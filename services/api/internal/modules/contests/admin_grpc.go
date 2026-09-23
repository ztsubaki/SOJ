package contests

import (
	"context"
	"errors"
	"github.com/ztsubaki/soj/services/api/internal/rpcutil"
	"strings"
	"time"

	sojv1 "github.com/ztsubaki/soj/proto/soj/v1"
	"github.com/ztsubaki/soj/services/api/internal/modules/auth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type AdminGRPCServer struct {
	sojv1.UnimplementedAdminContestServiceServer
	service *Service
	auth    *auth.Service
}

func NewAdminGRPCServer(service *Service, authService *auth.Service) *AdminGRPCServer {
	return &AdminGRPCServer{service: service, auth: authService}
}

func (s *AdminGRPCServer) ListContests(ctx context.Context, req *sojv1.AdminListContestsRequest) (*sojv1.AdminListContestsResponse, error) {
	if _, err := auth.RequireAdmin(ctx, s.auth); err != nil {
		return nil, err
	}
	page, size := rpcutil.Pagination(req.GetPage())
	want, err := statusText(req.Status)
	if err != nil {
		return nil, err
	}
	items, total, err := s.service.AdminList(ctx, req.GetQuery(), want, page, size)
	if err != nil {
		return nil, status.Error(codes.Internal, "list contests")
	}
	result := make([]*sojv1.Contest, 0, len(items))
	for _, item := range items {
		value, err := protoContest(item)
		if err != nil {
			return nil, status.Error(codes.Internal, "encode contest")
		}
		result = append(result, value)
	}
	return &sojv1.AdminListContestsResponse{Items: result, Page: &sojv1.PageMeta{Page: int32(page), PageSize: int32(size), Total: int64(total)}}, nil
}

func (s *AdminGRPCServer) CreateContest(ctx context.Context, req *sojv1.CreateContestRequest) (*sojv1.Contest, error) {
	actor, err := auth.RequireAdmin(ctx, s.auth)
	if err != nil {
		return nil, err
	}
	input, err := inputFromProto(req.GetContest())
	if err != nil {
		return nil, err
	}
	item, err := s.service.AdminCreate(ctx, actor.ID, input)
	if err != nil {
		return nil, contestError(err)
	}
	return protoContest(item)
}

func (s *AdminGRPCServer) GetContest(ctx context.Context, req *sojv1.AdminGetContestRequest) (*sojv1.Contest, error) {
	if _, err := auth.RequireAdmin(ctx, s.auth); err != nil {
		return nil, err
	}
	if err := requireID(req.GetId(), "contest id"); err != nil {
		return nil, err
	}
	item, err := s.service.AdminGet(ctx, req.GetId())
	if err != nil {
		return nil, contestError(err)
	}
	return protoContest(item)
}

func (s *AdminGRPCServer) ListRankings(ctx context.Context, req *sojv1.AdminListRankingsRequest) (*sojv1.AdminListRankingsResponse, error) {
	if _, err := auth.RequireAdmin(ctx, s.auth); err != nil {
		return nil, err
	}
	if err := requireID(req.GetContestId(), "contest id"); err != nil {
		return nil, err
	}
	p, n := rpcutil.Pagination(req.GetPage())
	items, total, err := s.service.Leaderboard(ctx, req.GetContestId(), true, p, n)
	if err != nil {
		return nil, contestError(err)
	}
	out := make([]*sojv1.LeaderboardEntry, 0, len(items))
	for _, item := range items {
		out = append(out, protoLeaderboardEntry(item))
	}
	return &sojv1.AdminListRankingsResponse{Items: out, Page: &sojv1.PageMeta{Page: int32(p), PageSize: int32(n), Total: int64(total)}}, nil
}

func (s *AdminGRPCServer) UpdateContest(ctx context.Context, req *sojv1.UpdateContestRequest) (*sojv1.Contest, error) {
	actor, err := auth.RequireAdmin(ctx, s.auth)
	if err != nil {
		return nil, err
	}
	if err := requireID(req.GetId(), "contest id"); err != nil {
		return nil, err
	}
	patch, err := patchFromProto(req)
	if err != nil {
		return nil, err
	}
	item, err := s.service.AdminUpdate(ctx, actor.ID, req.GetId(), patch)
	if err != nil {
		return nil, contestError(err)
	}
	return protoContest(item)
}

func (s *AdminGRPCServer) DeleteContest(ctx context.Context, req *sojv1.DeleteContestRequest) (*sojv1.Empty, error) {
	actor, err := auth.RequireAdmin(ctx, s.auth)
	if err != nil {
		return nil, err
	}
	if err = requireID(req.GetId(), "contest id"); err != nil {
		return nil, err
	}
	if err = s.service.AdminDelete(ctx, actor.ID, req.GetId()); err != nil {
		return nil, contestError(err)
	}
	return &sojv1.Empty{}, nil
}

func (s *AdminGRPCServer) DisqualifyParticipant(ctx context.Context, req *sojv1.DisqualifyParticipantRequest) (*sojv1.Empty, error) {
	actor, err := auth.RequireAdmin(ctx, s.auth)
	if err != nil {
		return nil, err
	}
	if err := requireID(req.GetContestId(), "contest id"); err != nil {
		return nil, err
	}
	if err := requireUserID(req.GetUserId()); err != nil {
		return nil, err
	}
	if err = s.service.AdminDisqualify(ctx, actor.ID, req.GetContestId(), req.GetUserId(), true); err != nil {
		return nil, contestError(err)
	}
	return &sojv1.Empty{}, nil
}

func (s *AdminGRPCServer) RestoreParticipant(ctx context.Context, req *sojv1.RestoreParticipantRequest) (*sojv1.Empty, error) {
	actor, err := auth.RequireAdmin(ctx, s.auth)
	if err != nil {
		return nil, err
	}
	if err := requireID(req.GetContestId(), "contest id"); err != nil {
		return nil, err
	}
	if err := requireUserID(req.GetUserId()); err != nil {
		return nil, err
	}
	if err = s.service.AdminDisqualify(ctx, actor.ID, req.GetContestId(), req.GetUserId(), false); err != nil {
		return nil, contestError(err)
	}
	return &sojv1.Empty{}, nil
}

func protoLeaderboardEntry(item LeaderboardEntry) *sojv1.LeaderboardEntry {
	result := &sojv1.LeaderboardEntry{Rank: int32(item.Rank), User: &sojv1.LeaderboardEntry_LeaderboardUser{Id: item.UserID, Username: item.Username}, Score: item.Score, Solved: int32(item.Solved), Submissions: int32(item.Submissions), Disqualified: item.Disqualified}
	result.ProblemScores = make([]*sojv1.LeaderboardProblemScore, 0, len(item.ProblemScores))
	for _, score := range item.ProblemScores {
		result.ProblemScores = append(result.ProblemScores, &sojv1.LeaderboardProblemScore{ProblemId: score.ProblemID, Score: score.Score})
	}
	if item.LastSubmission != nil {
		result.LastSubmission = timestamppb.New(*item.LastSubmission)
	}
	return result
}
func inputFromProto(value *sojv1.ContestInput) (AdminInput, error) {
	if value == nil || value.StartsAt == nil || value.EndsAt == nil {
		return AdminInput{}, status.Error(codes.InvalidArgument, "contest fields are required")
	}
	for _, id := range value.GetProblems() {
		if err := requireID(id, "problem id"); err != nil {
			return AdminInput{}, err
		}
	}
	policy, err := policyText(value.GetScoringPolicy())
	if err != nil {
		return AdminInput{}, err
	}
	return AdminInput{Title: value.GetTitle(), StartsAt: value.GetStartsAt().AsTime(), EndsAt: value.GetEndsAt().AsTime(), Description: value.GetDescription(), Problems: value.GetProblems(), Visible: value.GetVisible(), ScoringPolicy: policy}, nil
}

func requireID(value int64, field string) error {
	if value <= 0 {
		return status.Errorf(codes.InvalidArgument, "invalid %s", field)
	}
	return nil
}
func requireUserID(value int64) error {
	if value <= 0 {
		return status.Error(codes.InvalidArgument, "invalid user id")
	}
	return nil
}
func patchFromProto(req *sojv1.UpdateContestRequest) (AdminPatch, error) {
	patch := AdminPatch{Title: req.Title, Description: req.Description, Visible: req.Visible}
	if req.StartsAt != nil {
		v := req.StartsAt.AsTime()
		patch.StartsAt = &v
	}
	if req.EndsAt != nil {
		v := req.EndsAt.AsTime()
		patch.EndsAt = &v
	}
	if req.Problems != nil {
		v := append([]int64(nil), req.Problems.GetValues()...)
		patch.Problems = &v
	}
	if req.ScoringPolicy != nil {
		v, err := policyText(*req.ScoringPolicy)
		if err != nil {
			return AdminPatch{}, err
		}
		patch.ScoringPolicy = &v
	}
	return patch, nil
}
func statusText(value *sojv1.ContestStatus) (string, error) {
	if value == nil {
		return "", nil
	}
	switch *value {
	case sojv1.ContestStatus_CONTEST_STATUS_UPCOMING:
		return "upcoming", nil
	case sojv1.ContestStatus_CONTEST_STATUS_RUNNING:
		return "running", nil
	case sojv1.ContestStatus_CONTEST_STATUS_ENDED:
		return "ended", nil
	default:
		return "", status.Error(codes.InvalidArgument, "invalid contest status")
	}
}
func policyText(value sojv1.ScoringPolicy) (string, error) {
	switch value {
	case sojv1.ScoringPolicy_SCORING_POLICY_BEST:
		return "best", nil
	case sojv1.ScoringPolicy_SCORING_POLICY_LATEST:
		return "latest", nil
	default:
		return "", status.Error(codes.InvalidArgument, "invalid scoring policy")
	}
}
func protoContest(item AdminContest) (*sojv1.Contest, error) {
	state, err := protoStatus(item.Status)
	if err != nil {
		return nil, err
	}
	policy := sojv1.ScoringPolicy_SCORING_POLICY_BEST
	if item.ScoringPolicy == "latest" {
		policy = sojv1.ScoringPolicy_SCORING_POLICY_LATEST
	}
	visible := item.Visible
	result := &sojv1.Contest{Id: item.ID, Title: item.Title, Description: item.Description, Status: state, StartsAt: timestamppb.New(item.StartsAt), EndsAt: timestamppb.New(item.EndsAt), ParticipantCount: item.ParticipantCount, Visible: &visible, ScoringPolicy: &policy}
	for _, item := range item.Problems {
		difficulty, err := protoDifficulty(item.Difficulty)
		if err != nil {
			return nil, err
		}
		result.Problems = append(result.Problems, &sojv1.ProblemSummary{Id: item.ID, Title: item.Title, Difficulty: difficulty, Tags: item.Tags})
	}
	return result, nil
}
func protoStatus(value string) (sojv1.ContestStatus, error) {
	switch value {
	case "upcoming":
		return sojv1.ContestStatus_CONTEST_STATUS_UPCOMING, nil
	case "running":
		return sojv1.ContestStatus_CONTEST_STATUS_RUNNING, nil
	case "ended":
		return sojv1.ContestStatus_CONTEST_STATUS_ENDED, nil
	default:
		return sojv1.ContestStatus_CONTEST_STATUS_UNSPECIFIED, errors.New("invalid stored contest status")
	}
}
func protoDifficulty(value string) (sojv1.Difficulty, error) {
	switch value {
	case "Easy":
		return sojv1.Difficulty_DIFFICULTY_EASY, nil
	case "Medium":
		return sojv1.Difficulty_DIFFICULTY_MEDIUM, nil
	case "Hard":
		return sojv1.Difficulty_DIFFICULTY_HARD, nil
	default:
		return sojv1.Difficulty_DIFFICULTY_UNSPECIFIED, errors.New("invalid stored problem difficulty")
	}
}
func contestError(err error) error {
	switch {
	case errors.Is(err, ErrContestFixedFields):
		return status.Error(codes.FailedPrecondition, "contest fields are fixed")
	case errors.Is(err, ErrNotFound):
		return status.Error(codes.NotFound, "contest or participant not found")
	case errors.Is(err, ErrContestProblemNotFound):
		return status.Error(codes.NotFound, "one or more contest problems were not found")
	case errors.Is(err, ErrPublishedProblemReused):
		return status.Error(codes.FailedPrecondition, "a problem already published in an ended contest cannot be reused")
	case errors.Is(err, ErrInvalidContestInput):
		return status.Error(codes.InvalidArgument, err.Error())
	case strings.Contains(err.Error(), "duplicate key"):
		return status.Error(codes.AlreadyExists, "contest already exists")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.Unavailable, "contest operation unavailable")
	default:
		return status.Error(codes.InvalidArgument, "invalid contest input")
	}
}

var _ = time.Now
