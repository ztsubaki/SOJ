package auth

import (
	"context"
	"errors"
	"github.com/ztsubaki/soj/services/api/internal/rpcutil"

	sojv1 "github.com/ztsubaki/soj/proto/soj/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type AdminGRPCServer struct {
	sojv1.UnimplementedAdminUserServiceServer
	service *Service
}

func NewAdminGRPCServer(service *Service) *AdminGRPCServer { return &AdminGRPCServer{service: service} }

func (s *AdminGRPCServer) ListUsers(ctx context.Context, req *sojv1.AdminListUsersRequest) (*sojv1.AdminListUsersResponse, error) {
	if _, err := RequireAdmin(ctx, s.service); err != nil {
		return nil, err
	}
	page, size := rpcutil.Pagination(req.GetPage())
	userStatus, err := userStatusText(req.Status)
	if err != nil {
		return nil, err
	}
	role, err := roleText(req.Role)
	if err != nil {
		return nil, err
	}
	users, total, err := s.service.AdminListUsers(ctx, req.GetQuery(), userStatus, role, page, size)
	if err != nil {
		return nil, status.Error(codes.Internal, "list users")
	}
	items := make([]*sojv1.User, 0, len(users))
	for _, user := range users {
		items = append(items, protoUser(user))
	}
	return &sojv1.AdminListUsersResponse{Items: items, Page: &sojv1.PageMeta{Page: int32(page), PageSize: int32(size), Total: int64(total)}}, nil
}

func (s *AdminGRPCServer) CreateUser(ctx context.Context, req *sojv1.CreateUserRequest) (*sojv1.CreateUserResponse, error) {
	actor, err := RequireAdmin(ctx, s.service)
	if err != nil {
		return nil, err
	}
	user, err := s.service.AdminCreateUser(ctx, actor, req.GetUsername(), req.GetEmail())
	if err != nil {
		return nil, adminUserError(err)
	}
	return &sojv1.CreateUserResponse{User: protoUser(user)}, nil
}

func (s *AdminGRPCServer) ResetUserPassword(ctx context.Context, req *sojv1.AdminResetUserPasswordRequest) (*sojv1.AdminResetUserPasswordResponse, error) {
	actor, err := RequireAdmin(ctx, s.service)
	if err != nil {
		return nil, err
	}
	if err = requireUserID(req.GetId()); err != nil {
		return nil, err
	}
	password, err := s.service.AdminResetUserPassword(ctx, actor, req.GetId())
	if err != nil {
		return nil, adminUserError(err)
	}
	return &sojv1.AdminResetUserPasswordResponse{Password: password}, nil
}

func (s *AdminGRPCServer) GetUser(ctx context.Context, req *sojv1.AdminGetUserRequest) (*sojv1.User, error) {
	if _, err := RequireAdmin(ctx, s.service); err != nil {
		return nil, err
	}
	if err := requireUserID(req.GetId()); err != nil {
		return nil, err
	}
	user, err := s.service.GetUser(ctx, req.GetId())
	if err != nil {
		return nil, adminUserError(err)
	}
	return protoUser(user), nil
}

func (s *AdminGRPCServer) UpdateUser(ctx context.Context, req *sojv1.UpdateUserRequest) (*sojv1.User, error) {
	actor, err := RequireAdmin(ctx, s.service)
	if err != nil {
		return nil, err
	}
	if err := requireUserID(req.GetId()); err != nil {
		return nil, err
	}
	patch := AdminUserPatch{Email: req.Email}
	if req.Status != nil {
		value, err := userStatusText(req.Status)
		if err != nil {
			return nil, err
		}
		patch.Status = &value
	}
	if patch.Email == nil && patch.Status == nil {
		return nil, status.Error(codes.InvalidArgument, "user patch is empty")
	}
	user, err := s.service.AdminUpdateUser(ctx, actor, req.GetId(), patch)
	if err != nil {
		return nil, adminUserError(err)
	}
	return protoUser(user), nil
}

func (s *AdminGRPCServer) DeleteUser(ctx context.Context, req *sojv1.DeleteUserRequest) (*sojv1.Empty, error) {
	actor, err := RequireAdmin(ctx, s.service)
	if err != nil {
		return nil, err
	}
	if err = requireUserID(req.GetId()); err != nil {
		return nil, err
	}
	if err = s.service.AdminDeleteUser(ctx, actor, req.GetId()); err != nil {
		return nil, adminUserError(err)
	}
	return &sojv1.Empty{}, nil
}

func requireUserID(value int64) error {
	if value <= 0 {
		return status.Error(codes.InvalidArgument, "invalid user id")
	}
	return nil
}

func roleText(value *sojv1.Role) (string, error) {
	if value == nil {
		return "", nil
	}
	switch *value {
	case sojv1.Role_ROLE_USER:
		return RoleUser, nil
	case sojv1.Role_ROLE_ADMIN:
		return RoleAdmin, nil
	default:
		return "", status.Error(codes.InvalidArgument, "invalid user role")
	}
}

func userStatusText(value *sojv1.UserStatus) (string, error) {
	if value == nil {
		return "", nil
	}
	switch *value {
	case sojv1.UserStatus_USER_STATUS_ACTIVE:
		return "active", nil
	case sojv1.UserStatus_USER_STATUS_DISABLED:
		return "disabled", nil
	default:
		return "", status.Error(codes.InvalidArgument, "invalid user status")
	}
}

func adminUserError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return status.Error(codes.NotFound, "user not found")
	case errors.Is(err, ErrNotPermitted):
		return status.Error(codes.PermissionDenied, "user operation is not permitted")
	case errors.Is(err, ErrUsernameTaken):
		return status.Error(codes.AlreadyExists, "username or email already exists")
	case errors.Is(err, ErrInvalidRegistration):
		return status.Error(codes.InvalidArgument, "invalid user input")
	default:
		return status.Error(codes.Internal, "user operation failed")
	}
}
