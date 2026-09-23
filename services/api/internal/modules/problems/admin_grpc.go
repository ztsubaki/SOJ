package problems

import (
	"context"
	"errors"
	"github.com/ztsubaki/soj/services/api/internal/rpcutil"
	"strings"

	sojv1 "github.com/ztsubaki/soj/proto/soj/v1"
	"github.com/ztsubaki/soj/services/api/internal/modules/auth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type AdminGRPCServer struct {
	sojv1.UnimplementedAdminProblemServiceServer
	service *Service
	auth    *auth.Service
}

func NewAdminGRPCServer(service *Service, authService *auth.Service) *AdminGRPCServer {
	return &AdminGRPCServer{service: service, auth: authService}
}

func (s *AdminGRPCServer) ListProblems(ctx context.Context, req *sojv1.AdminListProblemsRequest) (*sojv1.AdminListProblemsResponse, error) {
	if _, err := auth.RequireAdmin(ctx, s.auth); err != nil {
		return nil, err
	}
	page, size := rpcutil.Pagination(req.GetPage())
	items, total, err := s.service.AdminList(ctx, req.GetQuery(), req.Enabled, page, size)
	if err != nil {
		return nil, status.Error(codes.Internal, "list problems")
	}
	result := make([]*sojv1.Problem, 0, len(items))
	for _, item := range items {
		value, err := protoProblem(item)
		if err != nil {
			return nil, status.Error(codes.Internal, "encode problem")
		}
		result = append(result, value)
	}
	return &sojv1.AdminListProblemsResponse{Items: result, Page: &sojv1.PageMeta{Page: int32(page), PageSize: int32(size), Total: int64(total)}}, nil
}

func (s *AdminGRPCServer) CreateProblem(ctx context.Context, req *sojv1.CreateProblemRequest) (*sojv1.Problem, error) {
	actor, err := auth.RequireAdmin(ctx, s.auth)
	if err != nil {
		return nil, err
	}
	input, err := fromProtoInput(req.GetProblem())
	if err != nil {
		return nil, err
	}
	item, err := s.service.AdminCreate(ctx, actor.ID, input)
	if err != nil {
		return nil, problemError(err)
	}
	return protoProblem(item)
}

func (s *AdminGRPCServer) GetProblem(ctx context.Context, req *sojv1.AdminGetProblemRequest) (*sojv1.Problem, error) {
	if _, err := auth.RequireAdmin(ctx, s.auth); err != nil {
		return nil, err
	}
	if err := requireProblemID(req.GetId()); err != nil {
		return nil, err
	}
	item, err := s.service.AdminGet(ctx, req.GetId())
	if err != nil {
		return nil, problemError(err)
	}
	return protoProblem(item)
}

func (s *AdminGRPCServer) UpdateProblem(ctx context.Context, req *sojv1.UpdateProblemRequest) (*sojv1.Problem, error) {
	actor, err := auth.RequireAdmin(ctx, s.auth)
	if err != nil {
		return nil, err
	}
	if err := requireProblemID(req.GetId()); err != nil {
		return nil, err
	}
	patch, err := fromProtoPatch(req.GetPatch())
	if err != nil {
		return nil, err
	}
	item, err := s.service.AdminUpdate(ctx, actor.ID, req.GetId(), patch)
	if err != nil {
		return nil, problemError(err)
	}
	return protoProblem(item)
}

func (s *AdminGRPCServer) DeleteProblem(ctx context.Context, req *sojv1.DeleteProblemRequest) (*sojv1.Empty, error) {
	actor, err := auth.RequireAdmin(ctx, s.auth)
	if err != nil {
		return nil, err
	}
	if err = requireProblemID(req.GetId()); err != nil {
		return nil, err
	}
	if err = s.service.AdminDelete(ctx, actor.ID, req.GetId()); err != nil {
		return nil, problemError(err)
	}
	return &sojv1.Empty{}, nil
}

func fromProtoInput(input *sojv1.ProblemInput) (AdminInput, error) {
	if input == nil {
		return AdminInput{}, status.Error(codes.InvalidArgument, "problem is required")
	}
	difficulty, err := difficultyText(input.GetDifficulty())
	if err != nil {
		return AdminInput{}, err
	}
	return AdminInput{Title: input.GetTitle(), Difficulty: difficulty, Tags: input.GetTags(), Partitions: input.GetPartitions(), Enabled: input.GetEnabled(), Content: input.GetContent()}, nil
}

func requireProblemID(value int64) error {
	if value <= 0 {
		return status.Error(codes.InvalidArgument, "invalid problem id")
	}
	return nil
}

func fromProtoPatch(patch *sojv1.ProblemPatch) (AdminPatch, error) {
	if patch == nil {
		return AdminPatch{}, status.Error(codes.InvalidArgument, "problem patch is required")
	}
	result := AdminPatch{Title: patch.Title, Enabled: patch.Enabled, Content: patch.Content}
	if patch.Difficulty != nil {
		value, err := difficultyText(*patch.Difficulty)
		if err != nil {
			return AdminPatch{}, err
		}
		result.Difficulty = &value
	}
	if patch.Partitions != nil {
		values := append([]string(nil), patch.Partitions.GetValues()...)
		result.Partitions = &values
	}
	switch update := patch.TagsUpdate.(type) {
	case *sojv1.ProblemPatch_Tags:
		values := append([]string(nil), update.Tags.GetValues()...)
		result.Tags = &values
	case *sojv1.ProblemPatch_ClearTags:
		values := []string{}
		result.Tags = &values
	}
	return result, nil
}

func protoProblem(item AdminProblem) (*sojv1.Problem, error) {
	difficulty, err := protoDifficulty(item.Difficulty)
	if err != nil {
		return nil, err
	}
	result := &sojv1.Problem{Id: item.ID, Title: item.Title, Difficulty: difficulty, Tags: item.Tags, Content: item.Content, Enabled: item.Enabled, Partitions: item.Partitions, CreatedAt: timestamppb.New(item.CreatedAt), UpdatedAt: timestamppb.New(item.UpdatedAt)}
	if item.Package != nil {
		manifest, err := structpb.NewStruct(item.Package.Manifest)
		if err != nil {
			return nil, err
		}
		result.Package = &sojv1.ProblemPackage{ArtifactId: item.Package.ArtifactID, Sha256: item.Package.SHA256, SizeBytes: item.Package.SizeBytes, Manifest: manifest, UploadedAt: timestamppb.New(item.Package.UploadedAt)}
	}
	return result, nil
}

func difficultyText(value sojv1.Difficulty) (string, error) {
	switch value {
	case sojv1.Difficulty_DIFFICULTY_EASY:
		return "Easy", nil
	case sojv1.Difficulty_DIFFICULTY_MEDIUM:
		return "Medium", nil
	case sojv1.Difficulty_DIFFICULTY_HARD:
		return "Hard", nil
	default:
		return "", status.Error(codes.InvalidArgument, "invalid problem difficulty")
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

func problemError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return status.Error(codes.NotFound, "problem not found")
	case errors.Is(err, ErrUsedByContest):
		return status.Error(codes.FailedPrecondition, "problem has been used by a contest")
	case strings.Contains(err.Error(), "duplicate key"):
		return status.Error(codes.AlreadyExists, "problem already exists")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.Unavailable, "problem operation unavailable")
	default:
		return status.Error(codes.InvalidArgument, "invalid problem input")
	}
}
