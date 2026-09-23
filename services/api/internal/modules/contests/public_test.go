package contests

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPublicErrorMapsDuplicateRegistration(t *testing.T) {
	if got := status.Code(publicError(ErrAlreadyRegistered)); got != codes.AlreadyExists {
		t.Fatalf("publicError(ErrAlreadyRegistered) = %s, want %s", got, codes.AlreadyExists)
	}
}
