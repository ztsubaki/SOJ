package stats

import (
	"context"

	sojv1 "github.com/ztsubaki/soj/proto/soj/v1"
	"github.com/ztsubaki/soj/services/api/internal/modules/auth"
	"github.com/ztsubaki/soj/services/api/internal/rpcauth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type GRPCServer struct {
	sojv1.UnimplementedStatsServiceServer
	service *Service
	auth    *auth.Service
}

func NewGRPCServer(service *Service, authService *auth.Service) *GRPCServer {
	return &GRPCServer{service: service, auth: authService}
}

func (s *GRPCServer) GetMyStats(ctx context.Context, _ *sojv1.GetMyStatsRequest) (*sojv1.MyStats, error) {
	if err := rpcauth.RequireCaller(ctx, rpcauth.CallerWebBFF); err != nil {
		return nil, err
	}
	user, err := auth.RequireCurrentUser(ctx, s.auth)
	if err != nil {
		return nil, err
	}
	var total, submissions, accepted int64
	var rank *int32
	err = s.service.pool.QueryRow(ctx, `WITH counts AS (
 SELECT u.id,u.role,COUNT(s.id)::bigint AS submissions,
 COUNT(s.id) FILTER (WHERE j.state='completed' AND j.verdict='accepted')::bigint AS accepted
 FROM users u LEFT JOIN submissions s ON s.user_id=u.id AND s.kind='contest'
 LEFT JOIN judge_jobs j ON j.id=s.current_job_id
 WHERE u.status='active' GROUP BY u.id
 ), ranked AS (
 SELECT id,submissions,RANK() OVER (ORDER BY accepted DESC,submissions ASC)::int AS rank
 FROM counts WHERE role='user'
 ) SELECT (SELECT count(*) FROM counts WHERE role='user'),c.submissions,c.accepted,
 CASE WHEN c.submissions=0 THEN NULL ELSE r.rank END
 FROM counts c LEFT JOIN ranked r ON r.id=c.id WHERE c.id=$1`, user.ID).Scan(&total, &submissions, &accepted, &rank)
	if err != nil {
		return nil, status.Error(codes.Internal, "get my stats")
	}
	result := &sojv1.MyStats{TotalUsers: total, SubmissionCount: submissions, AcceptedCount: accepted}
	if submissions > 0 {
		result.AcceptanceRate = float64(accepted) / float64(submissions)
	}
	if rank != nil {
		result.Rank = rank
	}
	return result, nil
}

func (s *GRPCServer) GetClusterStatus(ctx context.Context, _ *sojv1.GetClusterStatusRequest) (*sojv1.ClusterStatus, error) {
	if err := rpcauth.RequireCaller(ctx, rpcauth.CallerWebBFF); err != nil {
		return nil, err
	}
	snapshot, err := ReadClusterSnapshot(ctx, s.service.pool)
	if err != nil {
		return nil, status.Error(codes.Internal, "get cluster status")
	}
	return clusterStatus(snapshot), nil
}

func clusterStatus(snapshot ClusterSnapshot) *sojv1.ClusterStatus {
	result := &sojv1.ClusterStatus{TotalAgents: uint32(snapshot.Sessions), EmptyAgents: uint32(snapshot.Empty), NoQueueAgents: uint32(snapshot.NoQueue), QueuedAgents: uint32(snapshot.Queued)}
	if snapshot.UpdatedAt != nil {
		result.UpdatedAt = timestamppb.New(*snapshot.UpdatedAt)
	}
	return result
}
