package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPasswordWorkBoundedAndCancelable(t *testing.T) {
	for i := 0; i < cap(passwordWorkers); i++ {
		if err := acquirePasswordWorker(t.Context()); err != nil {
			t.Fatal(err)
		}
		defer func() { <-passwordWorkers }()
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := hashPassword(ctx, "test-password"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("saturated hash = %v, want deadline exceeded", err)
	}
}

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := hashPassword(t.Context(), "test-password")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword(t.Context(), hash, "test-password") || verifyPassword(t.Context(), hash, "wrong-password") {
		t.Fatal("password verification mismatch")
	}
}

func TestChangePasswordRejectsMalformedSessionBeforeWork(t *testing.T) {
	// No database is needed and no password worker should be acquired.
	for i := 0; i < cap(passwordWorkers); i++ {
		passwordWorkers <- struct{}{}
		defer func() { <-passwordWorkers }()
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := (&Service{}).ChangePassword(ctx, "forged", "old-password", "new-password"); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("forged session = %v, want invalid session", err)
	}
}

func TestValidPasswordRequiresPrintableASCII(t *testing.T) {
	tests := []struct {
		name     string
		password string
		valid    bool
	}{
		{name: "too short", password: "1234567", valid: false},
		{name: "minimum", password: "12345678", valid: true},
		{name: "printable ASCII", password: "abc XYZ!", valid: true},
		{name: "unicode", password: "密码测试一二三四", valid: false},
		{name: "control character", password: "abc\n1234", valid: false},
		{name: "maximum", password: strings.Repeat("a", 64), valid: true},
		{name: "too long", password: strings.Repeat("a", 65), valid: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validPassword(test.password); got != test.valid {
				t.Fatalf("validPassword(%q) = %v, want %v", test.password, got, test.valid)
			}
		})
	}
}

func TestIdentityFieldsRequireASCII(t *testing.T) {
	if !validUsername("user_name-1") {
		t.Fatal("ASCII username was rejected")
	}
	if validUsername("用户") {
		t.Fatal("Unicode username was accepted")
	}
	if !validEmail("user@example.test") {
		t.Fatal("ASCII email was rejected")
	}
	if validEmail("用户@example.test") {
		t.Fatal("Unicode email was accepted")
	}
}
