package main

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
)

// Business tests inject context explicitly; auth_test.go checks real signatures.
func testIdentityRequest(r *http.Request, subject string) *http.Request {
	r.Header.Set("X-User-Name", "forged-user-must-be-ignored")
	authorization := "Bearer unit-test-" + base64.RawURLEncoding.EncodeToString([]byte(subject))
	r.Header.Set("Authorization", authorization)
	return r.WithContext(context.WithValue(r.Context(), requestIdentityKey{}, requestIdentity{
		Subject: subject, Authorization: authorization,
	}))
}

func testSubjectFromAuthorization(header string) string {
	prefix := "Bearer unit-test-"
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return ""
	}
	return string(decoded)
}
