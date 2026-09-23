package auth

import (
	"regexp"
	"testing"
)

func TestRandomSessionTokenIsIndependentUUID(t *testing.T) {
	token, err := randomSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(token) {
		t.Fatalf("token %q is not a UUIDv4", token)
	}
	hash, ok := sessionTokenHash(token)
	if !ok || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(hash) {
		t.Fatalf("sessionTokenHash(%q) = (%q, %v), want SHA-256", token, hash, ok)
	}
	if _, ok := sessionTokenHash("not-a-uuid"); ok {
		t.Fatal("sessionTokenHash accepted a malformed token")
	}
}
