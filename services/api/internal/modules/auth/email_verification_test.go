package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

type recordingRegistrationEmailSender struct {
	recipients []string
	codes      []string
	err        error
}

func (s *recordingRegistrationEmailSender) SendRegistrationEmailVerification(_ context.Context, recipient, code string) error {
	if s.err != nil {
		return s.err
	}
	s.recipients = append(s.recipients, recipient)
	s.codes = append(s.codes, code)
	return nil
}

type blockingRegistrationEmailSender struct {
	started chan string
	finish  chan error
}

func (s *blockingRegistrationEmailSender) SendRegistrationEmailVerification(ctx context.Context, _ string, code string) error {
	select {
	case s.started <- code:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-s.finish:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestRegistrationEmailVerification(t *testing.T) {
	sender := &recordingRegistrationEmailSender{}
	service := &Service{emailSender: sender, emailVerifications: make(map[string]emailVerification)}
	const email = "runner@example.invalid"

	resendAfter, err := service.issueRegistrationEmailVerification(context.Background(), email)
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
	if len(sender.codes) != 1 || sender.recipients[0] != email || sender.codes[0] != verification.code {
		t.Fatalf("delivered verification = (%v, %v), want recipient %q and issued code", sender.recipients, sender.codes, email)
	}
	if _, err := service.issueRegistrationEmailVerification(context.Background(), email); !errors.Is(err, ErrEmailVerificationRateLimited) {
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
	if _, err := service.issueRegistrationEmailVerification(context.Background(), email); !errors.Is(err, ErrEmailVerificationRateLimited) {
		t.Fatalf("send after consumption = %v, want rate limited", err)
	}
	if err := service.validRegistrationEmailVerification(email, verification.code); !errors.Is(err, ErrEmailVerificationRequired) {
		t.Fatalf("consumed code = %v, want required", err)
	}
}

func TestRegistrationEmailVerificationIsNotIssuedWhenDeliveryFails(t *testing.T) {
	sender := &recordingRegistrationEmailSender{err: errors.New("SMTP unavailable")}
	service := &Service{emailSender: sender, emailVerifications: make(map[string]emailVerification)}
	const email = "runner@example.invalid"
	if _, err := service.issueRegistrationEmailVerification(context.Background(), email); !errors.Is(err, ErrEmailVerificationDelivery) {
		t.Fatalf("delivery failure = %v, want ErrEmailVerificationDelivery", err)
	}
	if _, exists := service.emailVerifications[email]; exists {
		t.Fatal("failed delivery retained a usable verification code")
	}
	sender.err = nil
	if _, err := service.issueRegistrationEmailVerification(context.Background(), email); err != nil {
		t.Fatalf("retry after delivery failure: %v", err)
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

func TestRegistrationEmailVerificationOldExpirationDoesNotRemoveReissue(t *testing.T) {
	service := &Service{
		emailSender:        &recordingRegistrationEmailSender{},
		emailVerifications: make(map[string]emailVerification),
	}
	const email = "runner@example.invalid"
	if _, err := service.issueRegistrationEmailVerification(context.Background(), email); err != nil {
		t.Fatalf("issue original verification: %v", err)
	}
	first := service.emailVerifications[email]
	// Advance only this issuance's age without sleeping or changing the clock
	// used by successful delivery. Its expiration still precedes the reissue.
	first.sentAt = time.Now().Add(-2 * registrationEmailVerificationCooldown)
	first.expiresAt = time.Now().Add(time.Minute)
	service.emailVerifications[email] = first
	service.emailVerificationExpirations[0].expiresAt = first.expiresAt
	if _, err := service.issueRegistrationEmailVerification(context.Background(), email); err != nil {
		t.Fatalf("reissue verification: %v", err)
	}
	second := service.emailVerifications[email]
	service.verificationMu.Lock()
	service.expireRegistrationEmailVerificationsLocked(first.expiresAt)
	service.verificationMu.Unlock()
	if got := service.emailVerifications[email]; got != second {
		t.Fatalf("old expiration changed the new verification: got %+v, want %+v", got, second)
	}
	if err := service.consumeRegistrationEmailVerification(email, second.code); err != nil {
		t.Fatalf("consume reissued code: %v", err)
	}
}

func TestRegistrationEmailVerificationDeliveryPreservesConcurrentConsumption(t *testing.T) {
	for _, test := range []struct {
		name        string
		deliveryErr error
	}{
		{name: "success"},
		{name: "failure", deliveryErr: errors.New("SMTP unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			sender := &blockingRegistrationEmailSender{started: make(chan string), finish: make(chan error)}
			const email = "runner@example.invalid"
			sentAt := time.Now().Add(-2 * registrationEmailVerificationCooldown)
			previous := emailVerification{code: "123456", sentAt: sentAt, expiresAt: sentAt.Add(registrationEmailVerificationTTL), generation: 1}
			service := &Service{
				emailSender:                 sender,
				emailVerifications:          map[string]emailVerification{email: previous},
				emailVerificationGeneration: 1,
				emailVerificationExpirations: []emailVerificationExpiration{{
					email: email, expiresAt: previous.expiresAt, generation: previous.generation,
				}},
			}
			result := make(chan error, 1)
			go func() {
				_, err := service.issueRegistrationEmailVerification(ctx, email)
				result <- err
			}()
			var deliveredCode string
			select {
			case deliveredCode = <-sender.started:
			case <-ctx.Done():
				t.Fatal("delivery did not start")
			}
			if _, err := service.issueRegistrationEmailVerification(ctx, email); !errors.Is(err, ErrEmailVerificationDelivery) {
				t.Fatalf("concurrent delivery = %v, want delivery in progress", err)
			}
			if err := service.consumeRegistrationEmailVerification(email, previous.code); err != nil {
				t.Fatalf("consume previous code during delivery: %v", err)
			}
			if err := service.consumeRegistrationEmailVerification(email, previous.code); !errors.Is(err, ErrEmailVerificationRequired) {
				t.Fatalf("consume previous code twice = %v, want required", err)
			}
			select {
			case sender.finish <- test.deliveryErr:
			case <-ctx.Done():
				t.Fatal("delivery did not finish")
			}
			var issueErr error
			select {
			case issueErr = <-result:
			case <-ctx.Done():
				t.Fatal("issuance did not finish")
			}
			if test.deliveryErr != nil {
				if !errors.Is(issueErr, ErrEmailVerificationDelivery) {
					t.Fatalf("failed issuance = %v, want delivery failure", issueErr)
				}
				if err := service.validRegistrationEmailVerification(email, previous.code); !errors.Is(err, ErrEmailVerificationRequired) {
					t.Fatalf("failed delivery restored a consumed code: %v", err)
				}
				return
			}
			if issueErr != nil {
				t.Fatalf("successful issuance: %v", issueErr)
			}
			if err := service.consumeRegistrationEmailVerification(email, deliveredCode); err != nil {
				t.Fatalf("consume delivered code: %v", err)
			}
			if _, err := service.issueRegistrationEmailVerification(ctx, email); !errors.Is(err, ErrEmailVerificationRateLimited) {
				t.Fatalf("resend after new-code consumption = %v, want rate limited", err)
			}
		})
	}
}

func TestRegistrationEmailVerificationIsConsumedOnceConcurrently(t *testing.T) {
	const email = "runner@example.invalid"
	service := &Service{emailVerifications: map[string]emailVerification{
		email: {code: "123456", expiresAt: time.Now().Add(registrationEmailVerificationTTL)},
	}}
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			results <- service.consumeRegistrationEmailVerification(email, "123456")
		}()
	}
	close(start)
	var consumed, required int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			consumed++
		case errors.Is(err, ErrEmailVerificationRequired):
			required++
		default:
			t.Fatalf("consume concurrently: %v", err)
		}
	}
	if consumed != 1 || required != 1 {
		t.Fatalf("concurrent consumption = %d successful, %d required; want one each", consumed, required)
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
