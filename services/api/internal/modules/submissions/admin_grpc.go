package submissions

import (
	"context"
	"errors"
	"fmt"
	"github.com/ztsubaki/soj/services/api/internal/rpcutil"
	"time"

	sojv1 "github.com/ztsubaki/soj/proto/soj/v1"
	"github.com/ztsubaki/soj/services/api/internal/modules/auth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type AdminGRPCServer struct {
	sojv1.UnimplementedAdminSubmissionServiceServer
	service *Service
	auth    *auth.Service
}

func NewAdminGRPCServer(service *Service, authService *auth.Service) *AdminGRPCServer {
	return &AdminGRPCServer{service: service, auth: authService}
}

func (s *AdminGRPCServer) ListSubmissions(ctx context.Context, req *sojv1.AdminListSubmissionsRequest) (*sojv1.AdminListSubmissionsResponse, error) {
	if _, err := auth.RequireAdmin(ctx, s.auth); err != nil {
		return nil, err
	}
	verdict, err := verdictText(req.Verdict)
	if err != nil {
		return nil, err
	}
	kind, err := kindText(req.Kind)
	if err != nil {
		return nil, err
	}
	page, size := rpcutil.Pagination(req.GetPage())
	items, total, err := s.service.ListAdmin(ctx, req.GetQuery(), verdict, req.GetUser(), req.GetProblem(), kind, page, size)
	if err != nil {
		return nil, status.Error(codes.Internal, "list submissions")
	}
	result := make([]*sojv1.Submission, 0, len(items))
	for _, item := range items {
		result = append(result, ProtoSubmission(item))
	}
	return &sojv1.AdminListSubmissionsResponse{Items: result, Page: pageMeta(page, size, total)}, nil
}

func (s *AdminGRPCServer) GetSubmission(ctx context.Context, req *sojv1.AdminGetSubmissionRequest) (*sojv1.AdminGetSubmissionResponse, error) {
	if _, err := auth.RequireAdmin(ctx, s.auth); err != nil {
		return nil, err
	}
	if err := requireSubmissionID(req.GetId()); err != nil {
		return nil, err
	}
	item, err := s.service.Get(ctx, req.GetId(), 0, true)
	if err != nil {
		return nil, status.Error(codes.NotFound, "submission not found")
	}
	meta, err := s.service.SourceMetadata(ctx, item.ID, 0, true)
	if err != nil {
		return nil, status.Error(codes.Internal, "read source metadata")
	}
	return &sojv1.AdminGetSubmissionResponse{Submission: ProtoSubmission(item), SourceMetadata: protoSource(meta), CurrentJobId: item.CurrentJobID, HasCurrentLog: item.CurrentLogArtifactID != nil}, nil
}

func (s *AdminGRPCServer) ListSubmissionJobs(ctx context.Context, req *sojv1.ListSubmissionJobsRequest) (*sojv1.ListSubmissionJobsResponse, error) {
	if _, err := auth.RequireAdmin(ctx, s.auth); err != nil {
		return nil, err
	}
	if err := requireSubmissionID(req.GetSubmissionId()); err != nil {
		return nil, err
	}
	page, size := rpcutil.Pagination(req.GetPage())
	items, total, err := s.service.ListJobs(ctx, req.GetSubmissionId(), page, size)
	if err == ErrNotFound {
		return nil, status.Error(codes.NotFound, "submission not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "list submission jobs")
	}
	result := make([]*sojv1.Job, 0, len(items))
	for _, item := range items {
		value, err := protoJob(item)
		if err != nil {
			return nil, status.Error(codes.Internal, "encode job")
		}
		result = append(result, value)
	}
	return &sojv1.ListSubmissionJobsResponse{Items: result, Page: pageMeta(page, size, total)}, nil
}

func (s *AdminGRPCServer) RejudgeSubmission(ctx context.Context, req *sojv1.RejudgeSubmissionRequest) (*sojv1.RejudgeSubmissionResponse, error) {
	actor, err := auth.RequireAdmin(ctx, s.auth)
	if err != nil {
		return nil, err
	}
	if err := requireSubmissionID(req.GetSubmissionId()); err != nil {
		return nil, err
	}
	item, jobID, err := s.service.RejudgeAsAdmin(ctx, actor.ID, req.GetSubmissionId())
	if err == ErrNotFound {
		return nil, status.Error(codes.NotFound, "submission not found")
	}
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, "submission cannot be rejudged")
	}
	return &sojv1.RejudgeSubmissionResponse{SubmissionId: item.ID, Status: statusProto(item.Status), JobId: jobID, CreatedAt: timestamppb.New(item.UpdatedAt)}, nil
}

func (s *AdminGRPCServer) CancelSubmission(ctx context.Context, req *sojv1.CancelSubmissionRequest) (*sojv1.GetMySubmissionResponse, error) {
	actor, err := auth.RequireAdmin(ctx, s.auth)
	if err != nil {
		return nil, err
	}
	if err := requireSubmissionID(req.GetSubmissionId()); err != nil {
		return nil, err
	}
	item, err := s.service.AdminCancel(ctx, actor.ID, req.GetSubmissionId())
	if err == ErrNotFound {
		return nil, status.Error(codes.NotFound, "submission not found")
	}
	if errors.Is(err, ErrCannotCancel) {
		return nil, status.Error(codes.FailedPrecondition, "submission cannot be cancelled")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "cancel submission")
	}
	meta, err := s.service.SourceMetadata(ctx, item.ID, 0, true)
	if err != nil {
		return nil, status.Error(codes.Internal, "read source metadata")
	}
	return &sojv1.GetMySubmissionResponse{Submission: ProtoSubmission(item), SourceMetadata: protoSource(meta), HasCurrentLog: item.CurrentLogArtifactID != nil}, nil
}

func protoJob(item Job) (*sojv1.Job, error) {
	manifest, err := structpb.NewStruct(item.Manifest)
	if err != nil {
		return nil, err
	}
	value := &sojv1.Job{Id: item.ID, SubmissionId: item.SubmissionID, State: jobStateProto(item.State), ExecutionSnapshot: &sojv1.JobExecutionSnapshot{PackageArtifactId: item.PackageArtifactID, PackageSha256: item.PackageSHA256, Manifest: manifest, EligiblePartitions: item.EligiblePartitions, AssignedPartition: item.AssignedPartition}, CreatedAt: timestamppb.New(item.CreatedAt), CancelRequestedAt: timestamp(item.CancelRequestedAt), ClaimedAt: timestamp(item.ClaimedAt), SubmittedAt: timestamp(item.SubmittedAt), StartedAt: timestamp(item.StartedAt), FinishedAt: timestamp(item.FinishedAt)}
	value.AgentSessionId, value.SlurmJobId = item.AgentSessionID, item.SlurmJobID
	if item.Verdict != nil {
		verdict := verdictProto(*item.Verdict)
		value.Verdict = &verdict
	}
	value.Score, value.StatusMessage = item.Score, item.Message
	if item.ResultArtifactID != nil {
		value.Result = &sojv1.ArtifactLink{Href: fmt.Sprintf("/api/v1/admin/submissions/%d/jobs/%d/result", item.SubmissionID, item.ID)}
	}
	if item.LogArtifactID != nil {
		value.Log = &sojv1.ArtifactLink{Href: fmt.Sprintf("/api/v1/admin/submissions/%d/jobs/%d/log", item.SubmissionID, item.ID)}
	}
	return value, nil
}

func timestamp(value *time.Time) *timestamppb.Timestamp {
	if value == nil {
		return nil
	}
	return timestamppb.New(*value)
}

func verdictText(value *sojv1.Verdict) (string, error) {
	if value == nil {
		return "", nil
	}
	result := map[sojv1.Verdict]string{sojv1.Verdict_VERDICT_ACCEPTED: "accepted", sojv1.Verdict_VERDICT_WRONG_ANSWER: "wrong_answer", sojv1.Verdict_VERDICT_COMPILE_ERROR: "compile_error", sojv1.Verdict_VERDICT_RUNTIME_ERROR: "runtime_error", sojv1.Verdict_VERDICT_TIME_LIMIT: "time_limit", sojv1.Verdict_VERDICT_MEMORY_LIMIT: "memory_limit"}[*value]
	if result == "" {
		return "", status.Error(codes.InvalidArgument, "invalid verdict")
	}
	return result, nil
}

func verdictProto(value string) sojv1.Verdict {
	for enum, text := range map[sojv1.Verdict]string{sojv1.Verdict_VERDICT_ACCEPTED: "accepted", sojv1.Verdict_VERDICT_WRONG_ANSWER: "wrong_answer", sojv1.Verdict_VERDICT_COMPILE_ERROR: "compile_error", sojv1.Verdict_VERDICT_RUNTIME_ERROR: "runtime_error", sojv1.Verdict_VERDICT_TIME_LIMIT: "time_limit", sojv1.Verdict_VERDICT_MEMORY_LIMIT: "memory_limit"} {
		if text == value {
			return enum
		}
	}
	return sojv1.Verdict_VERDICT_UNSPECIFIED
}

func kindText(value *sojv1.SubmissionKind) (string, error) {
	if value == nil {
		return "", nil
	}
	switch *value {
	case sojv1.SubmissionKind_SUBMISSION_KIND_CONTEST:
		return "contest", nil
	case sojv1.SubmissionKind_SUBMISSION_KIND_ADMIN_TEST:
		return "admin_test", nil
	default:
		return "", status.Error(codes.InvalidArgument, "invalid submission kind")
	}
}

func jobStateProto(value string) sojv1.JobState {
	return map[string]sojv1.JobState{"pending": sojv1.JobState_JOB_STATE_PENDING, "claimed": sojv1.JobState_JOB_STATE_CLAIMED, "submitted": sojv1.JobState_JOB_STATE_SUBMITTED, "running": sojv1.JobState_JOB_STATE_RUNNING, "finishing": sojv1.JobState_JOB_STATE_FINISHING, "completed": sojv1.JobState_JOB_STATE_COMPLETED, "cancelled": sojv1.JobState_JOB_STATE_CANCELLED, "internal_error": sojv1.JobState_JOB_STATE_INTERNAL_ERROR}[value]
}
