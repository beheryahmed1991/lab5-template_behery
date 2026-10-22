package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	jwtmiddleware "github.com/auth0/go-jwt-middleware/v3"
	"github.com/auth0/go-jwt-middleware/v3/validator"
)

func TestAuthMiddlewareJWTValidation(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var keyRequests atomic.Int32
	keys := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keyRequests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{
			"kty": "RSA", "kid": "local-test", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	defer keys.Close()
	config := authConfig{Issuer: "https://issuer.example/", Audience: "lab5-api", JWKSURL: keys.URL}
	protect, err := newAuthMiddleware(config)
	if err != nil {
		t.Fatal(err)
	}
	handler := protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err := jwtmiddleware.GetClaims[*validator.ValidatedClaims](r.Context())
		if err != nil || claims.RegisteredClaims.Subject != "auth0|test-user" {
			t.Error("validated subject was not available in request context")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	cases := []struct {
		name          string
		change        func(map[string]any)
		signer        *rsa.PrivateKey
		authorization string
		status        int
	}{
		{name: "valid", status: 204},
		{name: "valid cached keys", status: 204},
		{name: "missing token", authorization: "-", status: 401},
		{name: "malformed token", authorization: "Bearer invalid", status: 401},
		{name: "expired", change: func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() }, status: 401},
		{name: "wrong issuer", change: func(c map[string]any) { c["iss"] = "https://other.example/" }, status: 401},
		{name: "wrong audience", change: func(c map[string]any) { c["aud"] = "other-api" }, status: 401},
		{name: "wrong signature", signer: otherKey, status: 401},
		{name: "missing expiry", change: func(c map[string]any) { delete(c, "exp") }, status: 401},
		{name: "missing subject", change: func(c map[string]any) { delete(c, "sub") }, status: 401},
		{name: "not yet valid", change: func(c map[string]any) { c["nbf"] = time.Now().Add(time.Hour).Unix() }, status: 401},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims := map[string]any{
				"iss": config.Issuer, "aud": config.Audience, "sub": "auth0|test-user",
				"exp": time.Now().Add(time.Hour).Unix(),
			}
			if tc.change != nil {
				tc.change(claims)
			}
			signingKey := key
			if tc.signer != nil {
				signingKey = tc.signer
			}
			header := tc.authorization
			if header == "" {
				header = "Bearer " + signAuthTestToken(t, signingKey, claims, "RS256")
			}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/hotels", nil)
			if header != "-" {
				req.Header.Set("Authorization", header)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != tc.status {
				t.Fatalf("got %d, want %d: %s", response.Code, tc.status, response.Body.String())
			}
		})
	}
	if got := keyRequests.Load(); got != 1 {
		t.Fatalf("JWKS requests = %d, want 1 with cached keys", got)
	}
	token := signAuthTestToken(t, key, map[string]any{
		"iss": config.Issuer, "aud": config.Audience, "sub": "auth0|test-user",
		"exp": time.Now().Add(time.Hour).Unix(),
	}, "HS256")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/hotels", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != 401 {
		t.Fatalf("wrong algorithm got %d, want 401", response.Code)
	}
}

func TestAuthMiddlewarePublicPathsAndProtectedOptions(t *testing.T) {
	protect, err := newAuthMiddleware(authConfig{
		Issuer: "https://issuer.example/", Audience: "lab5-api", JWKSURL: "https://issuer.example/keys",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/manage/health", 204},
		{"GET", "/api/v1/authorize", 204},
		{"GET", "/api/v1/callback", 204},
		{"GET", "/api/v1/authorize/extra", 401},
		{"GET", "/api/v1/hotels", 401},
		{"OPTIONS", "/api/v1/hotels", 401},
		{"GET", "/api", 401},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
		if response.Code != tc.status {
			t.Errorf("%s %s: got %d, want %d", tc.method, tc.path, response.Code, tc.status)
		}
	}
}

func TestAuthConfigValidation(t *testing.T) {
	for _, tc := range []authConfig{
		{}, {Issuer: "https://issuer.example/", JWKSURL: "https://issuer.example/keys"},
		{Issuer: "http://remote.example/", Audience: "api", JWKSURL: "https://issuer.example/keys"},
		{Issuer: "https://issuer.example/", Audience: "api", JWKSURL: "http://remote.example/keys"},
		{Issuer: "https://user:password@issuer.example/", Audience: "api", JWKSURL: "https://issuer.example/keys"},
	} {
		if _, err := newAuthMiddleware(tc); err == nil {
			t.Errorf("invalid configuration accepted: %+v", tc)
		}
	}
	t.Setenv("AUTH0_ISSUER", "https://issuer.example/")
	t.Setenv("AUTH0_AUDIENCE", "api")
	t.Setenv("AUTH0_JWKS_URL", "https://issuer.example/keys")
	if _, err := newAuthMiddleware(authConfigFromEnv()); err != nil {
		t.Fatal(err)
	}
}

func signAuthTestToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any, algorithm string) string {
	t.Helper()
	headerJSON, err := json.Marshal(map[string]string{"alg": algorithm, "kid": "local-test", "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}
