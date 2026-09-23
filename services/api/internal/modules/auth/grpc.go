package auth

import (
	"context"
	"errors"
	"time"

	sojv1 "github.com/ztsubaki/soj/proto/soj/v1"
	"github.com/ztsubaki/soj/services/api/internal/rpcauth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type GRPCServer struct {
	sojv1.UnimplementedAuthServiceServer
	service *Service
}

func NewGRPCServer(service *Service) *GRPCServer { return &GRPCServer{service: service} }

func (s *GRPCServer) SendRegistrationEmailVerification(ctx context.Context, req *sojv1.SendRegistrationEmailVerificationRequest) (*sojv1.SendRegistrationEmailVerificationResponse, error) {
	if err := rpcauth.RequireCaller(ctx, rpcauth.CallerWebBFF); err != nil {
		return nil, err
	}
	usernameAvailable, emailAvailable, resendAfterSeconds, err := s.service.SendRegistrationEmailVerification(ctx, req.GetUsername(), req.GetEmail())
	if err != nil {
		return nil, authError(err)
	}
	return &sojv1.SendRegistrationEmailVerificationResponse{
		UsernameAvailable:  usernameAvailable,
		EmailAvailable:     emailAvailable,
		ResendAfterSeconds: resendAfterSeconds,
	}, nil
}

func (s *GRPCServer) Register(ctx context.Context, req *sojv1.RegisterRequest) (*sojv1.AuthSessionResponse, error) {
	if err := rpcauth.RequireCaller(ctx, rpcauth.CallerWebBFF); err != nil {
		return nil, err
	}
	credentials := req.GetCredentials()
	user, err := s.service.Register(ctx, credentials.GetUsername(), credentials.GetEmail(), credentials.GetPassword(), req.GetEmailVerificationCode())
	if err != nil {
		return nil, authError(err)
	}
	return s.sessionResponse(ctx, user)
}

func (s *GRPCServer) Login(ctx context.Context, req *sojv1.LoginRequest) (*sojv1.AuthSessionResponse, error) {
	if err := rpcauth.RequireCaller(ctx, rpcauth.CallerWebBFF, rpcauth.CallerAdminBFF); err != nil {
		return nil, err
	}
	user, token, expires, err := s.service.LoginSession(ctx, req.GetUsername(), req.GetPassword())
	if err != nil {
		return nil, authError(err)
	}
	return &sojv1.AuthSessionResponse{User: protoUser(user), SessionToken: token, ExpiresAt: timestamppb.New(expires)}, nil
}

func (s *GRPCServer) RefreshSession(ctx context.Context, _ *sojv1.RefreshSessionRequest) (*sojv1.RefreshSessionResponse, error) {
	if err := rpcauth.RequireCaller(ctx, rpcauth.CallerWebBFF, rpcauth.CallerAdminBFF); err != nil {
		return nil, err
	}
	token, err := rpcauth.SessionToken(ctx)
	if err != nil {
		return nil, err
	}
	nextToken, expiresAt, err := s.service.RefreshSession(ctx, token)
	if err != nil {
		return nil, authError(err)
	}
	return &sojv1.RefreshSessionResponse{SessionToken: nextToken, ExpiresAt: timestamppb.New(expiresAt)}, nil
}

func (s *GRPCServer) ChangePassword(ctx context.Context, req *sojv1.ChangePasswordRequest) (*sojv1.Empty, error) {
	if err := rpcauth.RequireCaller(ctx, rpcauth.CallerWebBFF, rpcauth.CallerAdminBFF); err != nil {
		return nil, err
	}
	token, err := rpcauth.SessionToken(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.service.ChangePassword(ctx, token, req.GetCurrentPassword(), req.GetNewPassword()); err != nil {
		return nil, authError(err)
	}
	return &sojv1.Empty{}, nil
}

func (s *GRPCServer) Logout(ctx context.Context, _ *sojv1.LogoutRequest) (*sojv1.Empty, error) {
	if err := rpcauth.RequireCaller(ctx, rpcauth.CallerWebBFF, rpcauth.CallerAdminBFF); err != nil {
		return nil, err
	}
	// Logout is intentionally idempotent. A missing session is indistinguishable
	// from an already-revoked one and is still a successful BFF operation.
	if token, err := rpcauth.SessionToken(ctx); err == nil {
		if err := s.service.Revoke(ctx, token); err != nil {
			return nil, status.Error(codes.Internal, "revoke session")
		}
	}
	return &sojv1.Empty{}, nil
}

func (s *GRPCServer) GetCurrentUser(ctx context.Context, _ *sojv1.GetCurrentUserRequest) (*sojv1.GetCurrentUserResponse, error) {
	if err := rpcauth.RequireCaller(ctx, rpcauth.CallerWebBFF, rpcauth.CallerAdminBFF); err != nil {
		return nil, err
	}
	token, err := rpcauth.SessionToken(ctx)
	if err != nil {
		return nil, err
	}
	user, expires, err := s.service.CurrentSession(ctx, token)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid browser session")
	}
	return &sojv1.GetCurrentUserResponse{User: protoUser(user), ExpiresAt: timestamppb.New(expires)}, nil
}

func (s *GRPCServer) sessionResponse(ctx context.Context, user User) (*sojv1.AuthSessionResponse, error) {
	token, expiresAt, err := s.service.CreateSession(ctx, user)
	if err != nil {
		return nil, status.Error(codes.Internal, "create session")
	}
	return &sojv1.AuthSessionResponse{User: protoUser(user), SessionToken: token, ExpiresAt: timestamppb.New(expiresAt)}, nil
}

func protoUser(user User) *sojv1.User {
	createdAt := user.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	role := sojv1.Role_ROLE_USER
	if user.Role == RoleAdmin {
		role = sojv1.Role_ROLE_ADMIN
	}
	state := sojv1.UserStatus_USER_STATUS_ACTIVE
	if user.Status == "disabled" {
		state = sojv1.UserStatus_USER_STATUS_DISABLED
	}
	return &sojv1.User{Id: user.ID, Username: user.Username, Email: user.Email, Role: role, Status: state, CreatedAt: timestamppb.New(createdAt)}
}

func authError(err error) error {
	switch {
	case errors.Is(err, ErrInvalidCredentials), errors.Is(err, ErrSessionInvalid):
		return status.Error(codes.Unauthenticated, "invalid credentials")
	case errors.Is(err, ErrUsernameTaken):
		return status.Error(codes.AlreadyExists, "username or email already exists")
	case errors.Is(err, ErrEmailVerificationRateLimited):
		return status.Error(codes.ResourceExhausted, "email verification is rate limited")
	case errors.Is(err, ErrEmailVerificationRequired), errors.Is(err, ErrEmailVerificationExpired), errors.Is(err, ErrEmailVerificationInvalid):
		return status.Error(codes.FailedPrecondition, "email verification is invalid")
	case errors.Is(err, ErrRegistrationEmailDomain):
		return status.Error(codes.InvalidArgument, "registration email domain is not allowed")
	case errors.Is(err, ErrInvalidRegistration), errors.Is(err, ErrPasswordMismatch):
		return status.Error(codes.InvalidArgument, "invalid credentials")
	default:
		return status.Error(codes.Internal, "authentication operation failed")
	}
}
