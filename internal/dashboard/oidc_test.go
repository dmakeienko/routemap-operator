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

const (
	testNS     = "default"
	testName   = "my-routemap"
	testIssuer = "https://accounts.example.com"
	testAud    = "my-client-id"
)

func newTestOIDC(t *testing.T) *OIDCConfig {
	t.Helper()
	o, err := NewOIDCConfig()
	if err != nil {
		t.Fatalf("NewOIDCConfig: %v", err)
	}
	return o
}

func makeToken(t *testing.T, o *OIDCConfig) string {
	t.Helper()
	tok, err := o.makeSessionToken(testNS, testName, testIssuer, testAud)
	if err != nil {
		t.Fatalf("makeSessionToken: %v", err)
	}
	return tok
}

func verify(o *OIDCConfig, tok string) bool {
	return o.verifySessionToken(tok, testNS, testName, testIssuer, testAud)
}

func TestMakeAndVerifySessionToken_Valid(t *testing.T) {
	o := newTestOIDC(t)
	tok := makeToken(t, o)
	if !verify(o, tok) {
		t.Error("valid token should verify successfully")
	}
}

func TestVerifySessionToken_WrongKey(t *testing.T) {
	o1 := newTestOIDC(t)
	o2 := newTestOIDC(t) // different random key

	tok := makeToken(t, o1)
	if verify(o2, tok) {
		t.Error("token signed with key1 should not verify with key2")
	}
}

func TestVerifySessionToken_TamperedPayload(t *testing.T) {
	o := newTestOIDC(t)
	tok := makeToken(t, o)

	parts := strings.SplitN(tok, ".", 2)
	payloadBytes, _ := base64.RawURLEncoding.DecodeString(parts[0])
	payloadBytes[0] ^= 0xFF // flip a byte
	tampered := base64.RawURLEncoding.EncodeToString(payloadBytes) + "." + parts[1]

	if verify(o, tampered) {
		t.Error("tampered token should not verify")
	}
}

func TestVerifySessionToken_BadSignature(t *testing.T) {
	o := newTestOIDC(t)
	tok := makeToken(t, o)

	parts := strings.SplitN(tok, ".", 2)
	badSig := base64.RawURLEncoding.EncodeToString([]byte("not-a-real-sig"))
	if verify(o, parts[0]+"."+badSig) {
		t.Error("token with bad signature should not verify")
	}
}

func TestVerifySessionToken_Expired(t *testing.T) {
	o := newTestOIDC(t)

	// Build a well-signed but already-expired token manually.
	payload := sessionPayload{
		Sub: "test", Exp: time.Now().Add(-time.Hour).Unix(),
		Namespace: testNS, Name: testName, Iss: testIssuer, Aud: testAud,
	}
	payloadBytes, _ := json.Marshal(payload)
	payloadEnc := base64.RawURLEncoding.EncodeToString(payloadBytes)

	mac := hmac.New(sha256.New, o.SessionKey)
	_, _ = mac.Write([]byte(payloadEnc))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	expired := payloadEnc + "." + sig
	if verify(o, expired) {
		t.Error("expired token should not verify")
	}
}

func TestVerifySessionToken_NoDot(t *testing.T) {
	o := newTestOIDC(t)
	if verify(o, "nodotinhere") {
		t.Error("token without dot separator should not verify")
	}
}

func TestVerifySessionToken_Empty(t *testing.T) {
	o := newTestOIDC(t)
	if verify(o, "") {
		t.Error("empty token should not verify")
	}
}

func TestSessionTokenEmbedsFutureExpiry(t *testing.T) {
	o := newTestOIDC(t)
	tok := makeToken(t, o)

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

func TestVerifySessionToken_WrongNamespace(t *testing.T) {
	o := newTestOIDC(t)
	tok := makeToken(t, o)
	if o.verifySessionToken(tok, "other-ns", testName, testIssuer, testAud) {
		t.Error("token should not verify for a different namespace")
	}
}

func TestVerifySessionToken_WrongName(t *testing.T) {
	o := newTestOIDC(t)
	tok := makeToken(t, o)
	if o.verifySessionToken(tok, testNS, "other-name", testIssuer, testAud) {
		t.Error("token should not verify for a different routemap name")
	}
}

func TestVerifySessionToken_WrongIssuer(t *testing.T) {
	o := newTestOIDC(t)
	tok := makeToken(t, o)
	if o.verifySessionToken(tok, testNS, testName, "https://evil.example.com", testAud) {
		t.Error("token should not verify for a different issuer")
	}
}

func TestVerifySessionToken_WrongAudience(t *testing.T) {
	o := newTestOIDC(t)
	tok := makeToken(t, o)
	if o.verifySessionToken(tok, testNS, testName, testIssuer, "other-client") {
		t.Error("token should not verify for a different audience")
	}
}

func TestValidateIssuerURL_Valid(t *testing.T) {
	if err := validateIssuerURL("https://accounts.google.com"); err != nil {
		t.Errorf("expected valid URL to pass, got: %v", err)
	}
}

func TestValidateIssuerURL_HTTP(t *testing.T) {
	if err := validateIssuerURL("http://accounts.google.com"); err == nil {
		t.Error("http:// issuer should be rejected")
	}
}

func TestValidateIssuerURL_Loopback(t *testing.T) {
	if err := validateIssuerURL("https://localhost"); err == nil {
		t.Error("loopback issuer should be rejected")
	}
}

func TestValidateIssuerURL_Invalid(t *testing.T) {
	if err := validateIssuerURL(":not-a-url"); err == nil {
		t.Error("malformed URL should be rejected")
	}
}
