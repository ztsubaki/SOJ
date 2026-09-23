package contests

import (
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestContestErrorKeepsProblemFailureDistinct(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want codes.Code
	}{
		{"missing problem", ErrContestProblemNotFound, codes.NotFound},
		{"reused published problem", ErrPublishedProblemReused, codes.FailedPrecondition},
		{"invalid input", ErrInvalidContestInput, codes.InvalidArgument},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := status.Code(contestError(test.err)); got != test.want {
				t.Fatalf("contestError(%v) = %s, want %s", test.err, got, test.want)
			}
		})
	}
}

func TestValidateInputReportsTheFailedField(t *testing.T) {
	tests := []struct {
		name  string
		input AdminInput
		want  string
	}{
		{"missing title", AdminInput{StartsAt: time.Now(), EndsAt: time.Now().Add(time.Hour), Problems: []int64{1}, ScoringPolicy: "best"}, "title is required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateInput(test.input)
			if !errors.Is(err, ErrInvalidContestInput) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateInput() = %v, want invalid input containing %q", err, test.want)
			}
		})
	}
}

func TestValidateInputAllowsAnEmptyProblemList(t *testing.T) {
	err := validateInput(AdminInput{
		Title:         "contest",
		StartsAt:      time.Now(),
		EndsAt:        time.Now().Add(time.Hour),
		Problems:      []int64{},
		ScoringPolicy: "best",
	})
	if err != nil {
		t.Fatalf("validateInput() = %v, want nil for an empty problem list", err)
	}
}
