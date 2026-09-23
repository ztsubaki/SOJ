// Package rpcauth validates the identity of Control Plane callers and carries
// that identity through unary RPC contexts. Browser sessions are deliberately
// separate from BFF service credentials.
package rpcauth

import (
	"context"
	"crypto/subtle"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type Caller string

const (
	CallerWebBFF   Caller = "web_bff"
	CallerAdminBFF Caller = "admin_bff"
	CallerJudge    Caller = "judge"
)

type contextKey struct{}

// UnaryInterceptor accepts exactly one service credential. Judge methods only
// accept the Execution Plane credential; all other methods only accept one of
// the two BFF credentials.
func UnaryInterceptor(agentToken, webBFFToken, adminBFFToken string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if _, ok := ctx.Deadline(); !ok {
			return nil, status.Error(codes.InvalidArgument, "request deadline is required")
		}
		credential, err := bearerCredential(ctx)
		if err != nil {
			return nil, err
		}
		caller := Caller("")
		if strings.HasPrefix(info.FullMethod, "/soj.judge.v1.") {
			if !constantTimeEqual(agentToken, credential) {
				return nil, status.Error(codes.Unauthenticated, "invalid service credentials")
			}
			caller = CallerJudge
		} else {
			switch {
			case constantTimeEqual(webBFFToken, credential):
				caller = CallerWebBFF
			case constantTimeEqual(adminBFFToken, credential):
				caller = CallerAdminBFF
			default:
				return nil, status.Error(codes.Unauthenticated, "invalid service credentials")
			}
		}
		return handler(context.WithValue(ctx, contextKey{}, caller), req)
	}
}

func CallerFromContext(ctx context.Context) (Caller, bool) {
	caller, ok := ctx.Value(contextKey{}).(Caller)
	return caller, ok
}

func RequireCaller(ctx context.Context, allowed ...Caller) error {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing service credentials")
	}
	for _, candidate := range allowed {
		if caller == candidate {
			return nil
		}
	}
	return status.Error(codes.PermissionDenied, "service is not authorized for this operation")
}

// SessionToken returns the opaque Browser session forwarded by a BFF. Its
// absence is a normal condition for public Auth RPCs, so callers choose when
// to require it.
func SessionToken(ctx context.Context) (string, error) {
	values := metadata.ValueFromIncomingContext(ctx, "x-soj-session")
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		return "", status.Error(codes.Unauthenticated, "missing browser session")
	}
	return values[0], nil
}

func bearerCredential(ctx context.Context) (string, error) {
	values := metadata.ValueFromIncomingContext(ctx, "authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return "", status.Error(codes.Unauthenticated, "invalid service credentials")
	}
	credential := strings.TrimPrefix(values[0], "Bearer ")
	if credential == "" {
		return "", status.Error(codes.Unauthenticated, "invalid service credentials")
	}
	return credential, nil
}

func constantTimeEqual(expected, actual string) bool {
	if expected == "" || len(expected) != len(actual) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) == 1
}
