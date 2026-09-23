package contests

import (
	"context"
	"errors"
	"github.com/ztsubaki/soj/services/api/internal/rpcutil"

	sojv1 "github.com/ztsubaki/soj/proto/soj/v1"
	"github.com/ztsubaki/soj/services/api/internal/modules/auth"
	"github.com/ztsubaki/soj/services/api/internal/rpcauth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type PublicGRPCServer struct {
	sojv1.UnimplementedContestServiceServer
	service *Service
	auth    *auth.Service
}

func NewPublicGRPCServer(service *Service, authService *auth.Service) *PublicGRPCServer {
	return &PublicGRPCServer{service: service, auth: authService}
}
func (s *PublicGRPCServer) ListContests(ctx context.Context, req *sojv1.ListContestsRequest) (*sojv1.ListContestsResponse, error) {
	if err := rpcauth.RequireCaller(ctx, rpcauth.CallerWebBFF); err != nil {
		return nil, err
	}
	p, n := rpcutil.Pagination(req.GetPage())
	want, err := statusText(req.Status)
	if err != nil {
		return nil, err
	}
	items, total, err := s.service.PublicList(ctx, req.GetQuery(), want, p, n)
	if err != nil {
		return nil, status.Error(codes.Internal, "list contests")
	}
	out := make([]*sojv1.Contest, 0, len(items))
	for _, item := range items {
		v, err := protoContest(item)
		if err != nil {
			return nil, status.Error(codes.Internal, "encode contest")
		}
		out = append(out, v)
	}
	return &sojv1.ListContestsResponse{Items: out, Page: &sojv1.PageMeta{Page: int32(p), PageSize: int32(n), Total: int64(total)}}, nil
}
func (s *PublicGRPCServer) GetContest(ctx context.Context, req *sojv1.GetContestRequest) (*sojv1.GetContestResponse, error) {
	if err := rpcauth.RequireCaller(ctx, rpcauth.CallerWebBFF); err != nil {
		return nil, err
	}
	if err := requireID(req.GetId(), "contest id"); err != nil {
		return nil, err
	}
	item, err := s.service.PublicGet(ctx, req.GetId())
	if err != nil {
		return nil, publicError(err)
	}
	v, err := protoContest(item)
	if err != nil {
		return nil, status.Error(codes.Internal, "encode contest")
	}
	var registered bool
	if userID := s.optionalUser(ctx); userID != 0 {
		if err := s.service.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM contest_registrations WHERE contest_id=$1 AND user_id=$2)`, req.GetId(), userID).Scan(&registered); err != nil {
			return nil, status.Error(codes.Internal, "read registration")
		}
	}
	return &sojv1.GetContestResponse{Contest: v, Registered: registered}, nil
}
func (s *PublicGRPCServer) GetLeaderboard(ctx context.Context, req *sojv1.GetLeaderboardRequest) (*sojv1.GetLeaderboardResponse, error) {
	if err := rpcauth.RequireCaller(ctx, rpcauth.CallerWebBFF); err != nil {
		return nil, err
	}
	if err := requireID(req.GetContestId(), "contest id"); err != nil {
		return nil, err
	}
	contest, err := s.service.PublicGet(ctx, req.GetContestId())
	if err != nil {
		return nil, publicError(err)
	}
	if contest.Status != "running" && contest.Status != "ended" {
		return nil, status.Error(codes.NotFound, "leaderboard is unavailable")
	}
	items, _, err := s.service.Leaderboard(ctx, req.GetContestId(), false, 1, 1000000)
	if err != nil {
		return nil, publicError(err)
	}
	out := make([]*sojv1.LeaderboardEntry, 0, len(items))
	for _, item := range items {
		out = append(out, protoLeaderboardEntry(item))
	}
	return &sojv1.GetLeaderboardResponse{Items: out}, nil
}
func (s *PublicGRPCServer) ListContestProblems(ctx context.Context, req *sojv1.ListContestProblemsRequest) (*sojv1.ListContestProblemsResponse, error) {
	if err := rpcauth.RequireCaller(ctx, rpcauth.CallerWebBFF); err != nil {
		return nil, err
	}
	if err := requireID(req.GetContestId(), "contest id"); err != nil {
		return nil, err
	}
	userID := s.optionalUser(ctx)
	items, err := s.service.AccessibleProblems(ctx, req.GetContestId(), userID)
	if err != nil {
		return nil, publicError(err)
	}
	out := make([]*sojv1.ProblemSummary, 0, len(items))
	for _, item := range items {
		d, err := protoDifficulty(item.Difficulty)
		if err != nil {
			return nil, status.Error(codes.Internal, "encode problem")
		}
		out = append(out, &sojv1.ProblemSummary{Id: item.ID, Title: item.Title, Difficulty: d, Tags: item.Tags, Runnable: item.Runnable})
	}
	return &sojv1.ListContestProblemsResponse{Items: out}, nil
}
func (s *PublicGRPCServer) GetContestProblem(ctx context.Context, req *sojv1.GetContestProblemRequest) (*sojv1.GetContestProblemResponse, error) {
	if err := rpcauth.RequireCaller(ctx, rpcauth.CallerWebBFF); err != nil {
		return nil, err
	}
	if err := requireID(req.GetContestId(), "contest id"); err != nil {
		return nil, err
	}
	if err := requireID(req.GetProblemId(), "problem id"); err != nil {
		return nil, err
	}
	item, content, err := s.service.AccessibleProblem(ctx, req.GetContestId(), req.GetProblemId(), s.optionalUser(ctx))
	if err != nil {
		return nil, publicError(err)
	}
	d, err := protoDifficulty(item.Difficulty)
	if err != nil {
		return nil, status.Error(codes.Internal, "encode problem")
	}
	return &sojv1.GetContestProblemResponse{Problem: &sojv1.Problem{Id: item.ID, Title: item.Title, Difficulty: d, Tags: item.Tags, Content: content, Runnable: item.Runnable}}, nil
}
func (s *PublicGRPCServer) RegisterForContest(ctx context.Context, req *sojv1.RegisterForContestRequest) (*sojv1.RegisterForContestResponse, error) {
	user, err := s.webUser(ctx)
	if err != nil {
		return nil, err
	}
	if err := requireID(req.GetContestId(), "contest id"); err != nil {
		return nil, err
	}
	registered, err := s.service.RegisterUser(ctx, req.GetContestId(), user.ID)
	if err != nil {
		return nil, publicError(err)
	}
	return &sojv1.RegisterForContestResponse{ContestId: req.GetContestId(), RegisteredAt: timestamppb.New(registered)}, nil
}
func (s *PublicGRPCServer) ListParticipants(ctx context.Context, req *sojv1.ListParticipantsRequest) (*sojv1.ListParticipantsResponse, error) {
	user, admin, err := s.participantViewer(ctx)
	if err != nil {
		return nil, err
	}
	if err := requireID(req.GetContestId(), "contest id"); err != nil {
		return nil, err
	}
	p, n := rpcutil.Pagination(req.GetPage())
	items, total, err := s.service.PublicParticipants(ctx, req.GetContestId(), user.ID, admin, p, n)
	if err != nil {
		return nil, publicError(err)
	}
	out := make([]*sojv1.Participant, 0, len(items))
	for _, item := range items {
		out = append(out, &sojv1.Participant{User: &sojv1.LeaderboardEntry_LeaderboardUser{Id: item.UserID, Username: item.Username}, RegisteredAt: timestamppb.New(item.RegisteredAt)})
	}
	return &sojv1.ListParticipantsResponse{Items: out, Page: &sojv1.PageMeta{Page: int32(p), PageSize: int32(n), Total: int64(total)}}, nil
}
func (s *PublicGRPCServer) participantViewer(ctx context.Context) (auth.User, bool, error) {
	caller, _ := rpcauth.CallerFromContext(ctx)
	if caller == rpcauth.CallerAdminBFF {
		user, err := auth.RequireAdmin(ctx, s.auth)
		return user, true, err
	}
	user, err := s.webUser(ctx)
	return user, false, err
}
func (s *PublicGRPCServer) webUser(ctx context.Context) (auth.User, error) {
	if err := rpcauth.RequireCaller(ctx, rpcauth.CallerWebBFF); err != nil {
		return auth.User{}, err
	}
	return auth.RequireCurrentUser(ctx, s.auth)
}
func (s *PublicGRPCServer) optionalUser(ctx context.Context) int64 {
	token, err := rpcauth.SessionToken(ctx)
	if err != nil {
		return 0
	}
	user, ok := s.auth.CurrentUser(ctx, token)
	if !ok {
		return 0
	}
	return user.ID
}
func publicError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return status.Error(codes.NotFound, "contest resource not found")
	case errors.Is(err, ErrAlreadyRegistered):
		return status.Error(codes.AlreadyExists, "contest registration already exists")
	case errors.Is(err, ErrForbidden):
		return status.Error(codes.FailedPrecondition, "contest resource is not available")
	default:
		return status.Error(codes.Internal, "contest operation failed")
	}
}
