package auth

import (
	"context"

	"github.com/ztsubaki/soj/services/api/internal/rpcauth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RequireCurrentUser performs final session validation inside the Control Plane.
// BFF route guards never substitute this check.
func RequireCurrentUser(ctx context.Context, service *Service) (User, error) {
	if err := rpcauth.RequireCaller(ctx, rpcauth.CallerWebBFF, rpcauth.CallerAdminBFF); err != nil {
		return User{}, err
	}
	token, err := rpcauth.SessionToken(ctx)
	if err != nil {
		return User{}, err
	}
	user, ok := service.CurrentUser(ctx, token)
	if !ok {
		return User{}, status.Error(codes.Unauthenticated, "invalid browser session")
	}
	return user, nil
}

func RequireAdmin(ctx context.Context, service *Service) (User, error) {
	if err := rpcauth.RequireCaller(ctx, rpcauth.CallerAdminBFF); err != nil {
		return User{}, err
	}
	user, err := RequireCurrentUser(ctx, service)
	if err != nil {
		return User{}, err
	}
	if user.Role != RoleAdmin {
		return User{}, status.Error(codes.PermissionDenied, "administrator role is required")
	}
	return user, nil
}
