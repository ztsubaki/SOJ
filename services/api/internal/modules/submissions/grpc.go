package submissions

import (
	"context"
	"github.com/ztsubaki/soj/services/api/internal/rpcutil"

	sojv1 "github.com/ztsubaki/soj/proto/soj/v1"
	"github.com/ztsubaki/soj/services/api/internal/modules/auth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type GRPCServer struct {
	sojv1.UnimplementedSubmissionServiceServer
	service *Service
	auth    *auth.Service
}

func NewGRPCServer(service *Service, authService *auth.Service) *GRPCServer {
	return &GRPCServer{service: service, auth: authService}
}

func (s *GRPCServer) ListMySubmissions(ctx context.Context, req *sojv1.ListMySubmissionsRequest) (*sojv1.ListMySubmissionsResponse, error) {
	user, err := auth.RequireCurrentUser(ctx, s.auth)
	if err != nil {
		return nil, err
	}
	page, size := rpcutil.Pagination(req.GetPage())
	var wantedStatus *string
	if req.Status != nil {
		value := statusText(req.GetStatus())
		if value == "" {
			return nil, status.Error(codes.InvalidArgument, "invalid submission status")
		}
		wantedStatus = &value
	}
	items, total, err := s.service.ListMine(ctx, user.ID, req.ContestId, req.ProblemId, wantedStatus, page, size)
	if err != nil {
		return nil, status.Error(codes.Internal, "list submissions")
	}
	result := make([]*sojv1.Submission, 0, len(items))
	for _, item := range items {
		result = append(result, ProtoSubmission(item))
	}
	return &sojv1.ListMySubmissionsResponse{Items: result, Page: pageMeta(page, size, total)}, nil
}

func (s *GRPCServer) GetMySubmission(ctx context.Context, req *sojv1.GetMySubmissionRequest) (*sojv1.GetMySubmissionResponse, error) {
	user, err := auth.RequireCurrentUser(ctx, s.auth)
	if err != nil {
		return nil, err
	}
	if err := requireSubmissionID(req.GetId()); err != nil {
		return nil, err
	}
	item, err := s.service.Get(ctx, req.GetId(), user.ID, false)
	if err != nil {
		return nil, status.Error(codes.NotFound, "submission not found")
	}
	meta, err := s.service.SourceMetadata(ctx, item.ID, user.ID, false)
	if err != nil {
		return nil, status.Error(codes.Internal, "read source metadata")
	}
	return &sojv1.GetMySubmissionResponse{Submission: ProtoSubmission(item), SourceMetadata: protoSource(meta), HasCurrentLog: item.CurrentLogArtifactID != nil}, nil
}

func requireSubmissionID(value int64) error {
	if value <= 0 {
		return status.Error(codes.InvalidArgument, "invalid submission id")
	}
	return nil
}

func ProtoSubmission(item Submission) *sojv1.Submission {
	value := &sojv1.Submission{Id: item.ID, Kind: kindProto(item.Kind), Problem: &sojv1.SubmissionProblem{Id: item.ProblemID, Title: item.ProblemTitle}, Status: statusProto(item.Status), CreatedAt: timestamppb.New(item.CreatedAt), UpdatedAt: timestamppb.New(item.UpdatedAt)}
	if item.ContestID != nil {
		value.ContestId = item.ContestID
	}
	if item.Score != nil {
		value.Score = item.Score
	}
	if item.JudgeMessage != nil {
		value.Message = item.JudgeMessage
	}
	return value
}

func protoSource(item ArtifactMetadata) *sojv1.SubmissionSourceMetadata {
	return &sojv1.SubmissionSourceMetadata{Filename: item.Filename, ContentType: item.ContentType, SizeBytes: item.SizeBytes, Sha256: item.SHA256}
}

func pageMeta(page, size, total int) *sojv1.PageMeta {
	return &sojv1.PageMeta{Page: int32(page), PageSize: int32(size), Total: int64(total)}
}

func kindProto(value string) sojv1.SubmissionKind {
	if value == "admin_test" {
		return sojv1.SubmissionKind_SUBMISSION_KIND_ADMIN_TEST
	}
	return sojv1.SubmissionKind_SUBMISSION_KIND_CONTEST
}

var submissionStatuses = map[sojv1.SubmissionStatus]string{sojv1.SubmissionStatus_SUBMISSION_STATUS_PENDING: "pending", sojv1.SubmissionStatus_SUBMISSION_STATUS_JUDGING: "judging", sojv1.SubmissionStatus_SUBMISSION_STATUS_ACCEPTED: "accepted", sojv1.SubmissionStatus_SUBMISSION_STATUS_WRONG_ANSWER: "wrong_answer", sojv1.SubmissionStatus_SUBMISSION_STATUS_COMPILE_ERROR: "compile_error", sojv1.SubmissionStatus_SUBMISSION_STATUS_RUNTIME_ERROR: "runtime_error", sojv1.SubmissionStatus_SUBMISSION_STATUS_TIME_LIMIT: "time_limit", sojv1.SubmissionStatus_SUBMISSION_STATUS_MEMORY_LIMIT: "memory_limit", sojv1.SubmissionStatus_SUBMISSION_STATUS_CANCELLED: "cancelled", sojv1.SubmissionStatus_SUBMISSION_STATUS_SYSTEM_ERROR: "system_error"}

func statusText(value sojv1.SubmissionStatus) string {
	if value == sojv1.SubmissionStatus_SUBMISSION_STATUS_UNSPECIFIED {
		return ""
	}
	return submissionStatuses[value]
}

func statusProto(value string) sojv1.SubmissionStatus {
	for enum, text := range submissionStatuses {
		if text == value {
			return enum
		}
	}
	return sojv1.SubmissionStatus_SUBMISSION_STATUS_PENDING
}
