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
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	routemapsv1alpha1 "github.com/dmakeienko/routemap-operator/api/v1alpha1"
	"github.com/dmakeienko/routemap-operator/internal/store"
)

const (
	sessionCookieName = "routemap_session"
	stateCookieName   = "routemap_state"
	nonceCookieName   = "routemap_nonce"
	sessionTTL        = 8 * time.Hour
)

// OIDCConfig holds per-process OIDC state shared across all Routemaps.
// Providers and oauth2.Config objects are cached by issuer+clientID.
type OIDCConfig struct {
	// SessionKey is used to sign session tokens (random per-process by default).
	SessionKey []byte

	mu        sync.RWMutex
	providers map[string]*oidcEntry
}

type oidcEntry struct {
	provider *gooidc.Provider
	config   oauth2.Config
}

// NewOIDCConfig returns an OIDCConfig with a random per-process session key.
func NewOIDCConfig() (*OIDCConfig, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return &OIDCConfig{
		SessionKey: key,
		providers:  make(map[string]*oidcEntry),
	}, nil
}

// Validate checks whether the request carries a valid session cookie.
// If not, it redirects to the IdP. Returns true when the request may proceed.
func (o *OIDCConfig) Validate(w http.ResponseWriter, r *http.Request, ns, name string, view store.RoutemapView) bool {
	auth := view.Spec.Auth
	if auth == nil {
		return true
	}

	// Verify the HMAC-signed session cookie, bound to this specific Routemap.
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		if o.verifySessionToken(cookie.Value, ns, name, auth.IssuerURL, auth.ClientID) {
			return true
		}
	}

	// Fetch or build oauth2 config.
	entry, err := o.getOrBuildEntry(r.Context(), auth)
	if err != nil {
		logf.Log.WithName("dashboard-oidc").Error(err, "Failed to build OIDC provider")
		http.Error(w, "OIDC configuration error", http.StatusInternalServerError)
		return false
	}

	// Generate state and nonce.
	state := randomToken()
	nonce := randomToken()

	secure := r.TLS != nil
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    state,
		Path:     fmt.Sprintf("/%s/%s", ns, name),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     nonceCookieName,
		Value:    nonce,
		Path:     fmt.Sprintf("/%s/%s", ns, name),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})

	http.Redirect(w, r, entry.config.AuthCodeURL(state, gooidc.Nonce(nonce)), http.StatusFound)
	return false
}

// handleOIDCCallback is called by Server when the IdP redirects back.
func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request, view store.RoutemapView) {
	auth := view.Spec.Auth
	if auth == nil || s.OIDC == nil {
		http.Error(w, "OIDC not configured", http.StatusBadRequest)
		return
	}

	log := logf.Log.WithName("dashboard-oidc")

	// Derive ns/name from the URL path (/{ns}/{name}/callback).
	parts := splitPath(r.URL.Path)
	ns, name := "", ""
	if len(parts) >= 2 {
		ns, name = parts[0], parts[1]
	}
	dashPath := "/"
	if ns != "" && name != "" {
		dashPath = fmt.Sprintf("/%s/%s", ns, name)
	}

	// Validate state.
	stateCookie, err := r.Cookie(stateCookieName)
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "Invalid state", http.StatusBadRequest)
		return
	}

	entry, err := s.OIDC.getOrBuildEntry(r.Context(), auth)
	if err != nil {
		http.Error(w, "OIDC configuration error", http.StatusInternalServerError)
		return
	}

	token, err := entry.config.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		log.Error(err, "Token exchange failed")
		http.Error(w, "Token exchange failed", http.StatusInternalServerError)
		return
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		http.Error(w, "No id_token in response", http.StatusInternalServerError)
		return
	}

	verifier := entry.provider.Verifier(&gooidc.Config{ClientID: auth.ClientID})
	nonceCookie, _ := r.Cookie(nonceCookieName)
	nonce := ""
	if nonceCookie != nil {
		nonce = nonceCookie.Value
	}

	idToken, err := verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		log.Error(err, "ID token verification failed")
		http.Error(w, "ID token verification failed", http.StatusUnauthorized)
		return
	}
	if idToken.Nonce != nonce {
		http.Error(w, "Nonce mismatch", http.StatusUnauthorized)
		return
	}

	// Set HMAC-signed session cookie, scoped to this Routemap's path.
	sessionVal, err := s.OIDC.makeSessionToken(ns, name, auth.IssuerURL, auth.ClientID)
	if err != nil {
		log.Error(err, "Failed to create session token")
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionVal,
		Path:     dashPath,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})

	http.Redirect(w, r, dashPath, http.StatusFound)
}

func (o *OIDCConfig) getOrBuildEntry(ctx context.Context, auth *routemapsv1alpha1.AuthSpec) (*oidcEntry, error) {
	if err := validateIssuerURL(auth.IssuerURL); err != nil {
		return nil, err
	}

	cacheKey := auth.IssuerURL + "|" + auth.ClientID

	o.mu.RLock()
	if e, ok := o.providers[cacheKey]; ok {
		o.mu.RUnlock()
		return e, nil
	}
	o.mu.RUnlock()

	provider, err := gooidc.NewProvider(ctx, auth.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("OIDC provider discovery failed: %w", err)
	}

	entry := &oidcEntry{
		provider: provider,
		config: oauth2.Config{
			ClientID:    auth.ClientID,
			RedirectURL: auth.RedirectURL,
			Endpoint:    provider.Endpoint(),
			Scopes:      []string{gooidc.ScopeOpenID, "profile", "email"},
		},
	}

	o.mu.Lock()
	o.providers[cacheKey] = entry
	o.mu.Unlock()
	return entry, nil
}

func randomToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

// sessionPayload is the signed body embedded in a session cookie.
// It is bound to a specific Routemap (Namespace/Name) and IdP (Iss/Aud)
// so that a token issued for one dashboard cannot be replayed on another.
type sessionPayload struct {
	Sub       string `json:"sub"`
	Exp       int64  `json:"exp"`
	Namespace string `json:"ns"`
	Name      string `json:"name"`
	Iss       string `json:"iss"`
	Aud       string `json:"aud"`
}

// validateIssuerURL rejects issuer URLs that could be used for SSRF:
// only https:// is allowed, and the resolved hostname must not be a
// loopback, link-local, or RFC1918 address.
func validateIssuerURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid issuer URL: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("issuer URL must use https, got %q", u.Scheme)
	}
	host := u.Hostname()
	addrs, err := net.LookupHost(host)
	if err != nil {
		// Treat resolution failure as a rejection to avoid blind DNS-rebind.
		return fmt.Errorf("cannot resolve issuer host %q: %w", host, err)
	}
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			continue
		}
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || isPrivate(ip) {
			return fmt.Errorf("issuer URL resolves to a private/loopback address (%s)", addr)
		}
	}
	return nil
}

// privateRanges lists RFC1918 + RFC4193 ULA ranges.
var privateRanges = func() []*net.IPNet {
	cidrs := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"100.64.0.0/10", // RFC6598 shared address
		"fc00::/7",      // IPv6 ULA
	}
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, _ := net.ParseCIDR(c)
		nets = append(nets, n)
	}
	return nets
}()

func isPrivate(ip net.IP) bool {
	for _, n := range privateRanges {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// makeSessionToken creates a base64(payload).base64(hmac) session token signed
// with OIDCConfig.SessionKey. The payload embeds the Routemap identity and IdP
// coordinates so that tokens cannot be replayed across dashboards.
func (o *OIDCConfig) makeSessionToken(ns, name, issuer, clientID string) (string, error) {
	sub := make([]byte, 16)
	if _, err := rand.Read(sub); err != nil {
		return "", err
	}
	payload := sessionPayload{
		Sub:       base64.RawURLEncoding.EncodeToString(sub),
		Exp:       time.Now().Add(sessionTTL).Unix(),
		Namespace: ns,
		Name:      name,
		Iss:       issuer,
		Aud:       clientID,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	payloadEnc := base64.RawURLEncoding.EncodeToString(payloadBytes)
	mac := hmac.New(sha256.New, o.SessionKey)
	_, _ = mac.Write([]byte(payloadEnc))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payloadEnc + "." + sig, nil
}

// verifySessionToken validates the HMAC signature, expiry, and Routemap binding
// of a session token. All five claims must match exactly.
func (o *OIDCConfig) verifySessionToken(tokenStr, ns, name, issuer, clientID string) bool {
	dot := -1
	for i := len(tokenStr) - 1; i >= 0; i-- {
		if tokenStr[i] == '.' {
			dot = i
			break
		}
	}
	if dot < 0 {
		return false
	}
	payloadEnc := tokenStr[:dot]
	sigEnc := tokenStr[dot+1:]

	mac := hmac.New(sha256.New, o.SessionKey)
	_, _ = mac.Write([]byte(payloadEnc))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(sigEnc), []byte(expected)) != 1 {
		return false
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadEnc)
	if err != nil {
		return false
	}
	var payload sessionPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return false
	}
	if time.Now().Unix() >= payload.Exp {
		return false
	}
	return payload.Namespace == ns &&
		payload.Name == name &&
		payload.Iss == issuer &&
		payload.Aud == clientID
}

func splitPath(path string) []string {
	var parts []string
	for _, p := range splitSlash(path) {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

func splitSlash(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}
