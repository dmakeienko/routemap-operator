# OIDC Authentication

The dashboard for any `Routemap` can be protected with OpenID Connect. When `spec.auth` is set, unauthenticated requests are redirected to the IdP; after a successful login a signed session cookie is issued.

## Supported flows

- Authorization Code Flow (standard OIDC)
- Any standards-compliant IdP: Google, Keycloak, Dex, Okta, GitHub (via OIDC proxy), etc.

## Setup

### 1. Register a client with your IdP

Register a redirect URI:

```
https://<your-dashboard-host>/{namespace}/{routemap-name}/callback
```

Note the **client ID** and **client secret**.

### 2. Create the Secret

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: routemap-oidc-secret
  namespace: default   # same namespace as the Routemap CR
type: Opaque
stringData:
  client-secret: "replace-with-real-secret"
```

### 3. Configure the Routemap

```yaml
spec:
  auth:
    issuerURL: https://accounts.google.com   # must expose /.well-known/openid-configuration
    clientID: "123456789-abc.apps.googleusercontent.com"
    clientSecretRef:
      name: routemap-oidc-secret
      key: client-secret
    redirectURL: https://routemap.example.com/default/my-routemap/callback
```

### 4. Expose the dashboard over HTTPS

The `Secure` flag is set on all cookies when `r.TLS != nil`. Put the operator behind a TLS-terminating ingress or use a service mesh for mTLS. Without TLS the session cookie will still work but will be sent over plaintext.

---

## Security model

| Mechanism | Detail |
|---|---|
| Session token | HMAC-SHA256 signed, base64url-encoded payload + signature (`payload.sig`). |
| Payload | JSON `{"sub":"<random-16-bytes>","exp":<unix-timestamp>}`. |
| Expiry | Embedded in the token itself — `sessionTTL = 8h`. No server-side state needed. |
| Signature key | Random 32-byte key generated at operator startup; rotated on pod restart. |
| Timing safety | Signature comparison uses `crypto/subtle.ConstantTimeCompare`. |
| State / nonce | Random 16-byte values stored as short-lived (`MaxAge: 600`) cookies to prevent CSRF and replay. |
| Cookie flags | `HttpOnly: true`, `SameSite: Lax`, `Secure: <true when TLS>`. |

**Note:** Because the signing key is in-process and not persisted, all sessions are invalidated when the operator pod restarts. For HA deployments with `--leader-elect=true` the dashboard runs on all replicas with independent keys — users may need to log in again after a failover.

---

## Provider caching

OIDC provider discovery (`GET /.well-known/openid-configuration`) is performed once per unique `issuerURL + clientID` pair and cached in memory for the lifetime of the process. This avoids repeated HTTP calls on every request.

---

## Scopes requested

The operator always requests `openid`, `profile`, and `email`. These are sufficient for nonce verification and user identification. No claims are stored beyond verifying the nonce and issuing the session cookie.

---

## Troubleshooting

| Symptom | Check |
|---|---|
| `OIDC configuration error` in browser | Issuer URL unreachable from the pod; check network policy and DNS. |
| `Invalid state` | State cookie expired (TTL 10 min) or browser blocked third-party cookies. |
| `Nonce mismatch` | ID token was replayed or the nonce cookie was dropped. |
| Session lost after pod restart | Expected — signing key is not persisted. |
| `No id_token in response` | IdP is not including the ID token in the token endpoint response; check IdP configuration. |
