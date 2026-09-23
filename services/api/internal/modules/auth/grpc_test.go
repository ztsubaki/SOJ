package auth

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAuthErrorMapsEmailVerificationFailuresToFailedPrecondition(t *testing.T) {
	for _, err := range []error{ErrEmailVerificationRequired, ErrEmailVerificationExpired, ErrEmailVerificationInvalid} {
		if got := status.Code(authError(err)); got != codes.FailedPrecondition {
			t.Fatalf("authError(%v) = %v, want %v", err, got, codes.FailedPrecondition)
		}
	}
}

func TestAuthErrorMapsRegistrationEmailDomainToInvalidArgument(t *testing.T) {
	err := authError(ErrRegistrationEmailDomain)
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Fatalf("authError(domain rejection) = %v, want %v", got, codes.InvalidArgument)
	}
	if got := status.Convert(err).Message(); got != "registration email domain is not allowed" {
		t.Fatalf("authError(domain rejection) message = %q", got)
	}
}
