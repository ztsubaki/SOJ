package auth

import (
	"errors"
	"testing"
	"time"
)

func TestRegistrationEmailVerification(t *testing.T) {
	service := &Service{emailVerifications: make(map[string]emailVerification)}
	const email = "runner@example.invalid"

	resendAfter, err := service.issueRegistrationEmailVerification(email)
	if err != nil {
		t.Fatalf("send verification: %v", err)
	}
	if resendAfter != 60 {
		t.Fatalf("resendAfter = %d, want 60", resendAfter)
	}
	verification := service.emailVerifications[email]
	if len(verification.code) != 6 {
		t.Fatalf("code %q is not six digits", verification.code)
	}
	if !verification.expiresAt.After(time.Now()) {
		t.Fatal("verification should not be expired")
	}
	if _, err := service.issueRegistrationEmailVerification(email); !errors.Is(err, ErrEmailVerificationRateLimited) {
		t.Fatalf("send during cooldown = %v, want rate limited", err)
	}
	if err := service.validRegistrationEmailVerification(email, "000000"); !errors.Is(err, ErrEmailVerificationInvalid) {
		t.Fatalf("invalid code = %v, want invalid", err)
	}
	if err := service.validRegistrationEmailVerification(email, "abcdef"); !errors.Is(err, ErrEmailVerificationInvalid) {
		t.Fatalf("non-numeric code = %v, want invalid", err)
	}
	if err := service.validRegistrationEmailVerification(email, verification.code); err != nil {
		t.Fatalf("valid code: %v", err)
	}
	if err := service.consumeRegistrationEmailVerification(email, verification.code); err != nil {
		t.Fatalf("consume code: %v", err)
	}
	if service.emailVerifications[email].code != "" {
		t.Fatal("consumed code was retained")
	}
	if _, err := service.issueRegistrationEmailVerification(email); !errors.Is(err, ErrEmailVerificationRateLimited) {
		t.Fatalf("send after consumption = %v, want rate limited", err)
	}
	if err := service.validRegistrationEmailVerification(email, verification.code); !errors.Is(err, ErrEmailVerificationRequired) {
		t.Fatalf("consumed code = %v, want required", err)
	}
}

func TestRegistrationEmailVerificationExpires(t *testing.T) {
	service := &Service{emailVerifications: map[string]emailVerification{
		"runner@example.invalid": {code: "123456", expiresAt: time.Now().Add(-time.Second)},
	}}
	if err := service.validRegistrationEmailVerification("runner@example.invalid", "123456"); !errors.Is(err, ErrEmailVerificationExpired) {
		t.Fatalf("expired code = %v, want expired", err)
	}
}

func TestRegistrationEmailDomainAllowlist(t *testing.T) {
	service := &Service{registrationEmailDomains: map[string]struct{}{
		"example.edu": {},
	}}
	for _, email := range []string{"runner@example.edu", "RUNNER@EXAMPLE.EDU"} {
		if err := service.validateRegistrationEmail(normalizeEmail(email)); err != nil {
			t.Errorf("validateRegistrationEmail(%q) = %v, want nil", email, err)
		}
	}
	for _, email := range []string{"runner@students.example.edu", "runner@example.com"} {
		if err := service.validateRegistrationEmail(normalizeEmail(email)); !errors.Is(err, ErrRegistrationEmailDomain) {
			t.Errorf("validateRegistrationEmail(%q) = %v, want domain rejection", email, err)
		}
	}
	if err := service.validateRegistrationEmail("runner@"); !errors.Is(err, ErrRegistrationEmailDomain) {
		t.Errorf("validateRegistrationEmail(runner@) = %v, want domain rejection", err)
	}

	unrestricted := &Service{}
	if err := unrestricted.validateRegistrationEmail("runner@example.com"); err != nil {
		t.Fatalf("unconfigured domain allowlist rejected a valid email: %v", err)
	}
}
