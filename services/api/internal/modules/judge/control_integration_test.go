package judge

import (
	"bytes"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	judgev1 "github.com/ztsubaki/soj/proto/soj/judge/v1"
	sojv1 "github.com/ztsubaki/soj/proto/soj/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestJudgeProtocolIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	const agent = "11111111-1111-4111-8111-111111111111"
	for _, q := range []string{`UPDATE judge_jobs SET state='pending',agent_session_id=NULL,assigned_partition=NULL,result_artifact_id=NULL,log_artifact_id=NULL,verdict=NULL,score=NULL,message=NULL,finished_at=NULL,cancel_requested_at=NULL,slurm_job_id=NULL,slurm_terminal_state=NULL WHERE id=100`, `DELETE FROM artifacts WHERE upload_job_id=100`, `UPDATE agent_sessions SET offline_at=now(),ended_at=now() WHERE partition='integration'`, `DELETE FROM agent_sessions WHERE id='11111111-1111-4111-8111-111111111111'`} {
		if _, err = pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	s := NewControlServer(pool, t.TempDir(), "service-token")
	registered, err := s.RegisterAgent(ctx, &judgev1.RegisterAgentRequest{AgentSessionId: agent, Partition: "integration"})
	if err != nil || registered.GetSession().GetState() != sojv1.AgentState_AGENT_STATE_ONLINE {
		t.Fatalf("register=%v %v", registered, err)
	}
	var wg sync.WaitGroup
	claims := make(chan *judgev1.ClaimJobsResponse, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := s.ClaimJobs(ctx, &judgev1.ClaimJobsRequest{AgentSessionId: agent})
			claims <- v
			errs <- e
		}()
	}
	wg.Wait()
	close(claims)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var assignment *judgev1.JobAssignment
	for v := range claims {
		if assignment == nil {
			assignment = v.Assignment
		} else if !proto.Equal(assignment, v.Assignment) {
			t.Fatal("concurrent claim changed assignment")
		}
	}
	if assignment.GetJobId() != 100 {
		t.Fatalf("assignment=%v", assignment)
	}
	if _, err = pool.Exec(ctx, `UPDATE agent_sessions SET drain_requested_at=now() WHERE id=$1`, agent); err != nil {
		t.Fatal(err)
	}
	replay, err := s.ClaimJobs(ctx, &judgev1.ClaimJobsRequest{AgentSessionId: agent})
	if err != nil || !proto.Equal(replay.Assignment, assignment) {
		t.Fatalf("draining replay=%v %v", replay, err)
	}
	slurm := "12345"
	if _, err = s.ReportJob(ctx, &judgev1.ReportJobRequest{AgentSessionId: agent, JobId: 100, State: sojv1.JobState_JOB_STATE_FINISHING, SlurmJobId: &slurm}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/internal/v1/judge/jobs/100/artifacts?kind=judge_result", bytes.NewBufferString("archived bytes are not parsed by the Control Plane"))
	req.Header.Set("Authorization", "Bearer service-token")
	req.Header.Set("X-SOJ-Agent-Session", agent)
	req.Header.Set("Content-Type", "application/octet-stream")
	rec := httptest.NewRecorder()
	s.ArtifactHTTP(rec, req, "100", "")
	if rec.Code != 201 {
		t.Fatalf("upload=%d %s", rec.Code, rec.Body)
	}
	var archive struct {
		ArtifactID int64 `json:"artifactId"`
	}
	if err = json.Unmarshal(rec.Body.Bytes(), &archive); err != nil {
		t.Fatal(err)
	}
	// Prove completion has no dependency on reading back the archived bytes.
	if err = os.Remove(filepath.Join(s.artifactRoot, strconv.FormatInt(archive.ArtifactID, 10))); err != nil {
		t.Fatal(err)
	}
	completed, err := s.CompleteJob(ctx, &judgev1.CompleteJobRequest{AgentSessionId: agent, JobId: 100, FinishedAt: timestamppb.Now(), SlurmTerminalState: sojv1.SlurmTerminalState_SLURM_TERMINAL_STATE_COMPLETED, ResultArtifactId: &archive.ArtifactID, Result: &judgev1.StructuredResult{Verdict: sojv1.Verdict_VERDICT_ACCEPTED, Score: "815.4"}})
	if err != nil || completed.State != sojv1.JobState_JOB_STATE_COMPLETED {
		t.Fatalf("complete=%v %v", completed, err)
	}
	var ended bool
	if err = pool.QueryRow(ctx, `SELECT ended_at IS NOT NULL FROM agent_sessions WHERE id=$1`, agent).Scan(&ended); err != nil || !ended {
		t.Fatalf("drain not atomic: %v", err)
	}
	// Any terminal report from the recorded owner replays actual state, even
	// after drain, and before validation of a contradictory request body.
	failed, err := s.FailJob(ctx, &judgev1.FailJobRequest{AgentSessionId: agent, JobId: 100})
	if err != nil || failed.State != sojv1.JobState_JOB_STATE_COMPLETED {
		t.Fatalf("replay=%v %v", failed, err)
	}
	if _, err = s.FailJob(ctx, &judgev1.FailJobRequest{AgentSessionId: "22222222-2222-4222-8222-222222222222", JobId: 100}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("nonowner=%v", err)
	}
}
func TestStaleAgentCannotReviveIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	const agent = "33333333-3333-4333-8333-333333333333"
	if _, err = pool.Exec(ctx, `DELETE FROM agent_sessions WHERE id=$1`, agent); err != nil {
		t.Fatal(err)
	}
	s := NewControlServer(pool, t.TempDir(), "token")
	if _, err = s.RegisterAgent(ctx, &judgev1.RegisterAgentRequest{AgentSessionId: agent, Partition: "stale-test"}); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE agent_sessions SET last_heartbeat_at=$2 WHERE id=$1`, agent, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReportAgentStatus(ctx, &judgev1.ReportAgentStatusRequest{AgentSessionId: agent}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("stale heartbeat=%v", err)
	}
	v, err := s.RegisterAgent(ctx, &judgev1.RegisterAgentRequest{AgentSessionId: agent, Partition: "stale-test"})
	if err != nil || v.Session.State != sojv1.AgentState_AGENT_STATE_OFFLINE {
		t.Fatalf("stale register=%v %v", v, err)
	}
}
