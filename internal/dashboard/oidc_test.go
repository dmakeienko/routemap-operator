/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package dashboard

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func newTestOIDC(t *testing.T) *OIDCConfig {
	t.Helper()
	o, err := NewOIDCConfig()
	if err != nil {
		t.Fatalf("NewOIDCConfig: %v", err)
	}
	return o
}

func TestMakeAndVerifySessionToken_Valid(t *testing.T) {
	o := newTestOIDC(t)
	tok, err := o.makeSessionToken()
	if err != nil {
		t.Fatalf("makeSessionToken: %v", err)
	}
	if !o.verifySessionToken(tok) {
		t.Error("valid token should verify successfully")
	}
}

func TestVerifySessionToken_WrongKey(t *testing.T) {
	o1 := newTestOIDC(t)
	o2 := newTestOIDC(t) // different random key

	tok, err := o1.makeSessionToken()
	if err != nil {
		t.Fatalf("makeSessionToken: %v", err)
	}
	if o2.verifySessionToken(tok) {
		t.Error("token signed with key1 should not verify with key2")
	}
}

func TestVerifySessionToken_TamperedPayload(t *testing.T) {
	o := newTestOIDC(t)
	tok, _ := o.makeSessionToken()

	parts := strings.SplitN(tok, ".", 2)
	payloadBytes, _ := base64.RawURLEncoding.DecodeString(parts[0])
	payloadBytes[0] ^= 0xFF // flip a byte
	tampered := base64.RawURLEncoding.EncodeToString(payloadBytes) + "." + parts[1]

	if o.verifySessionToken(tampered) {
		t.Error("tampered token should not verify")
	}
}

func TestVerifySessionToken_BadSignature(t *testing.T) {
	o := newTestOIDC(t)
	tok, _ := o.makeSessionToken()

	parts := strings.SplitN(tok, ".", 2)
	badSig := base64.RawURLEncoding.EncodeToString([]byte("not-a-real-sig"))
	if o.verifySessionToken(parts[0] + "." + badSig) {
		t.Error("token with bad signature should not verify")
	}
}

func TestVerifySessionToken_Expired(t *testing.T) {
	o := newTestOIDC(t)

	// Build a well-signed but already-expired token manually.
	payload := sessionPayload{Sub: "test", Exp: time.Now().Add(-time.Hour).Unix()}
	payloadBytes, _ := json.Marshal(payload)
	payloadEnc := base64.RawURLEncoding.EncodeToString(payloadBytes)

	mac := hmac.New(sha256.New, o.SessionKey)
	_, _ = mac.Write([]byte(payloadEnc))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	expired := payloadEnc + "." + sig
	if o.verifySessionToken(expired) {
		t.Error("expired token should not verify")
	}
}

func TestVerifySessionToken_NoDot(t *testing.T) {
	o := newTestOIDC(t)
	if o.verifySessionToken("nodotinhere") {
		t.Error("token without dot separator should not verify")
	}
}

func TestVerifySessionToken_Empty(t *testing.T) {
	o := newTestOIDC(t)
	if o.verifySessionToken("") {
		t.Error("empty token should not verify")
	}
}

func TestSessionTokenEmbedsFutureExpiry(t *testing.T) {
	o := newTestOIDC(t)
	tok, _ := o.makeSessionToken()

	parts := strings.SplitN(tok, ".", 2)
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var payload sessionPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Exp <= time.Now().Unix() {
		t.Errorf("exp %d should be in the future", payload.Exp)
	}
}
