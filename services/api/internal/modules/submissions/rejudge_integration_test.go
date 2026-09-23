package submissions

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRejudgeSwitchesCurrentJobProjectionIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for _, statement := range []string{
		`UPDATE submissions SET current_job_id=NULL WHERE id=9200`,
		`DELETE FROM judge_jobs WHERE submission_id=9200`,
		`DELETE FROM submissions WHERE id=9200`,
		`INSERT INTO agent_sessions(id,partition) VALUES ('agent-rejudge','rejudge') ON CONFLICT (id) DO UPDATE SET offline_at=NULL,ended_at=NULL,last_heartbeat_at=now()`,
		`INSERT INTO problems(id,title,content,difficulty,enabled,candidate_partitions,package_artifact_id,package_manifest,created_by) OVERRIDING SYSTEM VALUE VALUES (9200,'Rejudge problem','','Easy',true,ARRAY['rejudge'],100,'{"schemaVersion":1,"problemId":9200}',1) ON CONFLICT (id) DO UPDATE SET enabled=true,candidate_partitions=EXCLUDED.candidate_partitions,package_artifact_id=EXCLUDED.package_artifact_id,package_manifest=EXCLUDED.package_manifest`,
		`INSERT INTO submissions(id,user_id,problem_id,contest_id,kind,source_artifact_id) OVERRIDING SYSTEM VALUE VALUES (9200,2,9200,100,'contest',101)`,
		`INSERT INTO judge_jobs(id,submission_id,source_artifact_id,package_artifact_id,package_sha256,manifest,eligible_partitions,state,agent_session_id,assigned_partition,verdict,score,finished_at) OVERRIDING SYSTEM VALUE VALUES (9200,9200,101,100,'5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03','{"schemaVersion":1,"problemId":9200}',ARRAY['rejudge'],'completed','agent-rejudge','rejudge','accepted',10,now())`,
		`UPDATE submissions SET current_job_id=9200 WHERE id=9200`,
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	service := NewService(pool, t.TempDir(), DefaultMaxSourceSize)
	submission, newJobID, err := service.RejudgeAsAdmin(ctx, 1, 9200)
	if err != nil {
		t.Fatalf("RejudgeAsAdmin(): %v", err)
	}
	if newJobID <= 0 || newJobID == 9200 || submission.CurrentJobID == nil || *submission.CurrentJobID != newJobID || submission.Status != "pending" || submission.Score != nil {
		t.Fatalf("rejudge response = %#v, new job %d", submission, newJobID)
	}
	var state, oldState string
	if err := pool.QueryRow(ctx, `SELECT (SELECT state FROM judge_jobs WHERE id=$1), (SELECT state FROM judge_jobs WHERE id=9200)`, newJobID).Scan(&state, &oldState); err != nil {
		t.Fatal(err)
	}
	if state != "pending" || oldState != "completed" {
		t.Fatalf("job states after rejudge = (%q, %q)", state, oldState)
	}
	if _, err = pool.Exec(ctx, `UPDATE judge_jobs SET state='completed',agent_session_id='agent-rejudge',assigned_partition='rejudge',verdict='accepted',score=1,finished_at=now() WHERE id=$1`, newJobID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.AdminCancel(ctx, 1, submission.ID); !errors.Is(err, ErrCannotCancel) {
		t.Fatalf("AdminCancel() completed submission error = %v, want ErrCannotCancel", err)
	}
}
