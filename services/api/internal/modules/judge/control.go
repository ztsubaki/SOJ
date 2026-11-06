package judge

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	judgev1 "github.com/ztsubaki/soj/proto/soj/judge/v1"
	sojv1 "github.com/ztsubaki/soj/proto/soj/v1"
	"github.com/ztsubaki/soj/services/api/internal/modules/artifacts"
	"github.com/ztsubaki/soj/services/api/internal/rpcutil"
	"github.com/ztsubaki/soj/services/api/internal/validation"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"strings"
	"time"
)

const OfflineThreshold = 30 * time.Second
const heartbeatInterval = 5 * time.Second
const DefaultMaxAgentArtifactSize int64 = 64 << 20

type ControlServer struct {
	judgev1.UnimplementedJudgeControlServiceServer
	pool            *pgxpool.Pool
	artifactRoot    string
	serviceToken    string
	maxArtifactSize int64
}

func NewControlServer(pool *pgxpool.Pool, root, token string) *ControlServer {
	return &ControlServer{pool: pool, artifactRoot: root, serviceToken: token, maxArtifactSize: DefaultMaxAgentArtifactSize}
}
func (s *ControlServer) SetMaxArtifactSize(size int64) {
	if size > 0 {
		s.maxArtifactSize = size
	}
}

type session struct {
	id, partition, executor string
	created, heartbeat      time.Time
	drain, offline, ended   *time.Time
}

func readSession(ctx context.Context, tx pgx.Tx, id string) (session, error) {
	v := session{id: id}
	err := tx.QueryRow(ctx, `SELECT partition,executor,registered_at,last_heartbeat_at,drain_requested_at,offline_at,ended_at FROM agent_sessions WHERE id=$1 FOR UPDATE`, id).Scan(&v.partition, &v.executor, &v.created, &v.heartbeat, &v.drain, &v.offline, &v.ended)
	return v, err
}
func (v session) live() bool {
	return v.offline == nil && v.ended == nil && time.Since(v.heartbeat) < OfflineThreshold
}
func (v session) state() sojv1.AgentState {
	if v.offline != nil {
		return sojv1.AgentState_AGENT_STATE_OFFLINE
	}
	if v.ended != nil {
		return sojv1.AgentState_AGENT_STATE_ENDED
	}
	if v.drain != nil {
		return sojv1.AgentState_AGENT_STATE_DRAINING
	}
	return sojv1.AgentState_AGENT_STATE_ONLINE
}
func (v session) view() *judgev1.AgentSessionView {
	return &judgev1.AgentSessionView{Id: v.id, Partition: v.partition, Executor: rpcutil.ExecutorProto(v.executor), State: v.state(), CreatedAt: timestamppb.New(v.created), LastHeartbeatAt: timestamppb.New(v.heartbeat)}
}
func endDrained(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `UPDATE agent_sessions SET ended_at=now() WHERE id=$1 AND drain_requested_at IS NOT NULL AND offline_at IS NULL AND ended_at IS NULL AND last_heartbeat_at>now()-$2::interval AND NOT EXISTS(SELECT 1 FROM judge_jobs WHERE agent_session_id=$1 AND state IN ('claimed','submitted','running','finishing'))`, id, OfflineThreshold.String())
	return err
}
func retire(ctx context.Context, tx pgx.Tx, id string) error {
	if _, err := tx.Exec(ctx, `UPDATE judge_jobs SET state='internal_error',message='agent session offline',finished_at=COALESCE(finished_at,now()) WHERE agent_session_id=$1 AND state IN ('claimed','submitted','running','finishing')`, id); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE agent_sessions SET offline_at=COALESCE(offline_at,now()),ended_at=COALESCE(ended_at,now()) WHERE id=$1 AND offline_at IS NULL AND ended_at IS NULL`, id)
	return err
}
func (s *ControlServer) RegisterAgent(ctx context.Context, r *judgev1.RegisterAgentRequest) (*judgev1.RegisterAgentResponse, error) {
	executor := rpcutil.ExecutorText(r.GetExecutor())
	if !isUUID(r.GetAgentSessionId()) || !isASCII(r.GetPartition()) || executor == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid registration")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// Serialize registration for one partition, including when no row exists yet.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, r.GetPartition()); err != nil {
		return nil, err
	}
	v, err := readSession(ctx, tx, r.GetAgentSessionId())
	if err == nil {
		if v.partition != r.GetPartition() {
			return nil, status.Error(codes.InvalidArgument, "partition changed")
		}
		if v.executor != executor {
			return nil, status.Error(codes.InvalidArgument, "executor changed")
		}
		if v.offline == nil && v.ended == nil && !v.live() {
			if err = retire(ctx, tx, v.id); err != nil {
				return nil, err
			}
			v, err = readSession(ctx, tx, v.id)
		}
	} else if errors.Is(err, pgx.ErrNoRows) {
		rows, e := tx.Query(ctx, `SELECT id FROM agent_sessions WHERE partition=$1 AND offline_at IS NULL AND ended_at IS NULL FOR UPDATE`, r.GetPartition())
		if e != nil {
			return nil, e
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return nil, e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
		for _, id := range ids {
			old, e := readSession(ctx, tx, id)
			if e != nil {
				return nil, e
			}
			if old.live() {
				return nil, status.Error(codes.AlreadyExists, "partition already has an active agent")
			}
			if e = retire(ctx, tx, id); e != nil {
				return nil, e
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO agent_sessions(id,partition,executor) VALUES($1,$2,$3)`, r.GetAgentSessionId(), r.GetPartition(), executor); err != nil {
			return nil, err
		}
		v, err = readSession(ctx, tx, r.GetAgentSessionId())
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &judgev1.RegisterAgentResponse{Session: v.view(), HeartbeatIntervalMs: uint64(heartbeatInterval.Milliseconds()), OfflineThresholdMs: uint64(OfflineThreshold.Milliseconds())}, nil
}
func (s *ControlServer) ReportAgentStatus(ctx context.Context, r *judgev1.ReportAgentStatusRequest) (*judgev1.ReportAgentStatusResponse, error) {
	if !isUUID(r.GetAgentSessionId()) {
		return nil, status.Error(codes.InvalidArgument, "invalid session id")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	v, err := readSession(ctx, tx, r.GetAgentSessionId())
	if err != nil || !v.live() {
		return nil, status.Error(codes.FailedPrecondition, "session unavailable")
	}
	if _, err = tx.Exec(ctx, `UPDATE agent_sessions SET last_heartbeat_at=now() WHERE id=$1`, v.id); err != nil {
		return nil, err
	}
	if err = endDrained(ctx, tx, v.id); err != nil {
		return nil, err
	}
	v, err = readSession(ctx, tx, v.id)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM judge_jobs WHERE agent_session_id=$1 AND cancel_requested_at IS NOT NULL AND state IN ('claimed','submitted','running','finishing') ORDER BY created_at,id`, v.id)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &judgev1.ReportAgentStatusResponse{State: v.state(), CancelJobIds: ids, ServerTime: timestamppb.Now()}, nil
}
func (s *ControlServer) ClaimJobs(ctx context.Context, r *judgev1.ClaimJobsRequest) (*judgev1.ClaimJobsResponse, error) {
	if !isUUID(r.GetAgentSessionId()) {
		return nil, status.Error(codes.InvalidArgument, "invalid session id")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	v, err := readSession(ctx, tx, r.GetAgentSessionId())
	if err != nil || !v.live() {
		return nil, status.Error(codes.FailedPrecondition, "session unavailable")
	}
	var id int64
	var phase string
	err = tx.QueryRow(ctx, `SELECT id,state FROM judge_jobs WHERE agent_session_id=$1 AND state IN ('claimed','submitted') FOR UPDATE`, v.id).Scan(&id, &phase)
	if err == nil && phase == "submitted" {
		return nil, status.Error(codes.FailedPrecondition, "session already has a submitted job")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if v.drain != nil {
			return nil, status.Error(codes.FailedPrecondition, "session draining")
		}
		// A bounded FIFO head keeps dense partitions cheap without scanning an
		// unrelated backlog. Sparse partitions fall back to indexed candidates.
		err = tx.QueryRow(ctx, `WITH head AS MATERIALIZED (
 SELECT id,created_at FROM judge_jobs WHERE state='pending' ORDER BY created_at,id LIMIT 64
)
SELECT j.id FROM head h JOIN judge_jobs j ON j.id=h.id
WHERE j.state='pending' AND j.eligible_partitions @> ARRAY[$1]::text[] AND (j.manifest ? 'docker')=($2='docker')
ORDER BY h.created_at,h.id FOR UPDATE OF j SKIP LOCKED LIMIT 1`, v.partition, v.executor).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			// Materialization prevents LIMIT from choosing a full FIFO-index walk.
			// Recheck state on the locked row, not only in the candidate snapshot.
			err = tx.QueryRow(ctx, `WITH eligible AS MATERIALIZED (
 SELECT id,created_at FROM judge_jobs WHERE state='pending' AND eligible_partitions @> ARRAY[$1]::text[] AND (manifest ? 'docker')=($2='docker')
)
SELECT j.id FROM eligible e JOIN judge_jobs j ON j.id=e.id
WHERE j.state='pending' AND (j.manifest ? 'docker')=($2='docker')
ORDER BY e.created_at,e.id FOR UPDATE OF j SKIP LOCKED LIMIT 1`, v.partition, v.executor).Scan(&id)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			var pending, eligible int64
			if err = tx.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM judge_jobs WHERE state='pending'),
 (SELECT count(*) FROM judge_jobs WHERE state='pending' AND eligible_partitions @> ARRAY[$1]::text[] AND (manifest ? 'docker')=($2='docker'))`, v.partition, v.executor).Scan(&pending, &eligible); err != nil {
				return nil, err
			}
			return &judgev1.ClaimJobsResponse{State: v.state(), PendingJobCount: uint32(pending), EligiblePendingJobCount: uint32(eligible)}, nil
		}
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `UPDATE judge_jobs SET state='claimed',agent_session_id=$2,assigned_partition=$3,claimed_at=now() WHERE id=$1`, id, v.id, v.partition); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	assignment, err := s.loadAssignment(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if rpcutil.ExecutorText(assignment.GetExecutor()) != v.executor {
		return nil, status.Error(codes.FailedPrecondition, "job executor does not match session")
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &judgev1.ClaimJobsResponse{Assignment: assignment, State: v.state()}, nil
}

type ownedJob struct {
	state     string
	execution *string
	cancel    bool
}

func lockOwned(ctx context.Context, tx pgx.Tx, agent string, id int64) (session, ownedJob, error) {
	v, err := readSession(ctx, tx, agent)
	if err != nil {
		return v, ownedJob{}, status.Error(codes.PermissionDenied, "job is not owned by session")
	}
	var j ownedJob
	err = tx.QueryRow(ctx, `SELECT state,execution_id,cancel_requested_at IS NOT NULL FROM judge_jobs WHERE id=$1 AND agent_session_id=$2 FOR UPDATE`, id, agent).Scan(&j.state, &j.execution, &j.cancel)
	if errors.Is(err, pgx.ErrNoRows) {
		err = status.Error(codes.PermissionDenied, "job is not owned by session")
	}
	return v, j, err
}
func (s *ControlServer) ReportJob(ctx context.Context, r *judgev1.ReportJobRequest) (*judgev1.ReportJobResponse, error) {
	target := protoState(r.GetState())
	if !validAgentJob(r.GetAgentSessionId(), r.GetJobId()) || target == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid job report")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	v, j, err := lockOwned(ctx, tx, r.GetAgentSessionId(), r.GetJobId())
	if err != nil {
		return nil, err
	}
	if !v.live() || !activeState(j.state) {
		return nil, status.Error(codes.FailedPrecondition, "job unavailable")
	}
	if j.execution != nil && r.GetExecutionId() != "" && *j.execution != r.GetExecutionId() {
		return nil, status.Error(codes.FailedPrecondition, "execution id changed")
	}
	if target != "claimed" && j.execution == nil && r.GetExecutionId() == "" {
		return nil, status.Error(codes.InvalidArgument, "execution id required")
	}
	if stateOrder(target) <= stateOrder(j.state) {
		return &judgev1.ReportJobResponse{State: toProtoState(j.state), ExecutionId: j.execution}, nil
	}
	if _, err = tx.Exec(ctx, `UPDATE judge_jobs SET state=$2,execution_id=COALESCE(execution_id,NULLIF($3,'')),submitted_at=COALESCE(submitted_at,now()),started_at=CASE WHEN $2 IN ('running','finishing') THEN COALESCE(started_at,now()) ELSE started_at END,finishing_at=CASE WHEN $2='finishing' THEN COALESCE(finishing_at,now()) ELSE finishing_at END WHERE id=$1`, r.GetJobId(), target, r.GetExecutionId()); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	if j.execution == nil {
		j.execution = r.ExecutionId
	}
	return &judgev1.ReportJobResponse{State: toProtoState(target), ExecutionId: j.execution}, nil
}
func isPrintableASCII(value string) bool {
	return value != "" && strings.IndexFunc(value, func(r rune) bool { return r < 32 || r > 126 }) < 0
}
func (s *ControlServer) terminal(ctx context.Context, agent string, id int64, finish *timestamppb.Timestamp, target string, apply func(pgx.Tx, ownedJob) error) (sojv1.JobState, error) {
	if !validAgentJob(agent, id) {
		return 0, status.Error(codes.InvalidArgument, "invalid job identity")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	v, j, err := lockOwned(ctx, tx, agent, id)
	if err != nil {
		return 0, err
	}
	if !activeState(j.state) {
		return toProtoState(j.state), nil
	}
	if !v.live() {
		return 0, status.Error(codes.FailedPrecondition, "session unavailable")
	}
	if finish == nil || !finish.IsValid() {
		return 0, status.Error(codes.InvalidArgument, "finished time required")
	}
	if err = apply(tx, j); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `UPDATE judge_jobs SET state=$2,finished_at=$3 WHERE id=$1`, id, target, finish.AsTime()); err != nil {
		return 0, err
	}
	if err = endDrained(ctx, tx, agent); err != nil {
		return 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return toProtoState(target), nil
}
func (s *ControlServer) FailJob(ctx context.Context, r *judgev1.FailJobRequest) (*judgev1.FailJobResponse, error) {
	state, err := s.terminal(ctx, r.GetAgentSessionId(), r.GetJobId(), r.GetFinishedAt(), "internal_error", func(tx pgx.Tx, j ownedJob) error {
		if !isPrintableASCII(r.GetStatusMessage()) || len(r.GetStatusMessage()) > 1024 {
			return status.Error(codes.InvalidArgument, "invalid failure message")
		}
		_, err := tx.Exec(ctx, `UPDATE judge_jobs SET message=$2 WHERE id=$1`, r.GetJobId(), r.GetStatusMessage())
		return err
	})
	return &judgev1.FailJobResponse{State: state}, err
}
func (s *ControlServer) CancelJob(ctx context.Context, r *judgev1.CancelJobRequest) (*judgev1.CancelJobResponse, error) {
	state, err := s.terminal(ctx, r.GetAgentSessionId(), r.GetJobId(), r.GetFinishedAt(), "cancelled", func(tx pgx.Tx, j ownedJob) error {
		if !j.cancel {
			return status.Error(codes.FailedPrecondition, "cancellation not requested")
		}
		return nil
	})
	return &judgev1.CancelJobResponse{State: state}, err
}
func (s *ControlServer) CompleteJob(ctx context.Context, r *judgev1.CompleteJobRequest) (*judgev1.CompleteJobResponse, error) {
	state, err := s.terminal(ctx, r.GetAgentSessionId(), r.GetJobId(), r.GetFinishedAt(), "completed", func(tx pgx.Tx, j ownedJob) error {
		if j.state != "finishing" {
			return status.Error(codes.FailedPrecondition, "job is not finishing")
		}
		terminal := terminalStateValue(r.GetExecutionTerminalState())
		result := r.GetResult()
		verdict := strings.ToLower(strings.TrimPrefix(result.GetVerdict().String(), "VERDICT_"))
		if result == nil || !validDecimal(result.GetScore()) || result.Message != nil && (!isPrintableASCII(result.GetMessage()) || len(result.GetMessage()) > 1024) {
			return status.Error(codes.InvalidArgument, "invalid structured result")
		}
		switch terminal {
		case "COMPLETED", "FAILED":
			if !(verdict == "accepted" || verdict == "wrong_answer" || verdict == "compile_error" || verdict == "runtime_error") || r.ResultArtifactId == nil {
				return status.Error(codes.InvalidArgument, "invalid ordinary result")
			}
		case "TIMEOUT", "OUT_OF_MEMORY":
			want := "time_limit"
			if terminal == "OUT_OF_MEMORY" {
				want = "memory_limit"
			}
			if verdict != want || result.GetScore() != "0" || result.Message != nil || r.ResultArtifactId != nil {
				return status.Error(codes.InvalidArgument, "invalid resource limit result")
			}
		default:
			return status.Error(codes.InvalidArgument, "invalid execution terminal state")
		}
		for _, a := range []struct {
			id   *int64
			kind string
		}{{r.ResultArtifactId, "judge_result"}, {r.LogArtifactId, "judge_log"}} {
			if a.id != nil {
				tag, e := tx.Exec(ctx, `UPDATE artifacts SET state='available',available_at=now() WHERE id=$1 AND state='pending' AND kind=$2 AND upload_job_id=$3 AND upload_agent_session_id=$4 AND sha256 IS NOT NULL AND size_bytes>0`, *a.id, a.kind, r.GetJobId(), r.GetAgentSessionId())
				if e != nil {
					return e
				}
				if tag.RowsAffected() != 1 {
					return status.Error(codes.FailedPrecondition, "artifact not valid for job")
				}
			}
		}
		_, err := tx.Exec(ctx, `UPDATE judge_jobs SET execution_terminal_state=$2,verdict=$3,score=$4,message=$5,result_artifact_id=$6,log_artifact_id=$7 WHERE id=$1`, r.GetJobId(), terminal, verdict, result.GetScore(), result.Message, r.ResultArtifactId, r.LogArtifactId)
		if err != nil {
			return err
		}
		for _, id := range []*int64{r.ResultArtifactId, r.LogArtifactId} {
			if id != nil {
				if err := artifacts.Retain(ctx, tx, *id); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return &judgev1.CompleteJobResponse{State: state}, err
}
func (s *ControlServer) loadAssignment(ctx context.Context, tx pgx.Tx, jobID int64) (*judgev1.JobAssignment, error) {
	var id, submissionID, packageID, sourceID int64
	var sessionID, partition, packageSHA, sourceSHA, sourceType, sourceFilename string
	var manifestJSON []byte
	var created time.Time
	var packageSize, sourceSize int64
	err := tx.QueryRow(ctx, `SELECT j.id,j.submission_id,j.agent_session_id,j.assigned_partition,j.manifest,j.created_at,pa.id,pa.sha256,pa.size_bytes,sa.id,sa.sha256,sa.size_bytes,sa.content_type,s.source_filename FROM judge_jobs j JOIN submissions s ON s.id=j.submission_id JOIN artifacts pa ON pa.id=j.package_artifact_id JOIN artifacts sa ON sa.id=j.source_artifact_id WHERE j.id=$1`, jobID).Scan(&id, &submissionID, &sessionID, &partition, &manifestJSON, &created, &packageID, &packageSHA, &packageSize, &sourceID, &sourceSHA, &sourceSize, &sourceType, &sourceFilename)
	if err != nil {
		return nil, err
	}
	packageManifest, err := validation.ParsePackageManifest(manifestJSON)
	if err != nil {
		return nil, err
	}
	var values map[string]any
	if err = json.Unmarshal(manifestJSON, &values); err != nil {
		return nil, err
	}
	manifest, err := structpb.NewStruct(values)
	if err != nil {
		return nil, err
	}
	return &judgev1.JobAssignment{JobId: id, SubmissionId: submissionID, AgentSessionId: sessionID, AssignedPartition: partition, Executor: rpcutil.ExecutorProto(packageManifest.Executor()), Package: &judgev1.ArtifactReference{ArtifactId: packageID, Sha256: packageSHA, SizeBytes: uint64(packageSize)}, Submission: &judgev1.ArtifactReference{ArtifactId: sourceID, Sha256: sourceSHA, SizeBytes: uint64(sourceSize)}, Manifest: manifest, SubmissionFilename: sourceFilename, SubmissionContentType: sourceType, CreatedAt: timestamppb.New(created)}, nil
}

func validDecimal(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	dot := false
	for i, r := range value {
		if r == '.' {
			if dot || i == 0 || i == len(value)-1 {
				return false
			}
			dot = true
			continue
		}
		if r < '0' || r > '9' {
			return false
		}
		if i == 0 && r == '0' && len(value) > 1 && value[1] != '.' {
			return false
		}
	}
	return true
}
