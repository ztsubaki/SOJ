package submissions

import (
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	sojv1 "github.com/ztsubaki/soj/proto/soj/v1"
)

func TestJobExecutorSnapshotIntegration(t *testing.T) {
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
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	for _, test := range []struct {
		name, manifest, executionID string
		executor                    sojv1.AgentExecutor
	}{
		{"slurm", `{"schemaVersion":1,"problemId":100}`, "12345", sojv1.AgentExecutor_AGENT_EXECUTOR_SLURM},
		{"docker", `{"schemaVersion":1,"problemId":100,"docker":{"image":"sha256:` + strings.Repeat("a", 64) + `","cpus":1,"memoryMb":128,"timeLimitSeconds":10}}`, strings.Repeat("b", 64), sojv1.AgentExecutor_AGENT_EXECUTOR_DOCKER},
	} {
		t.Run(test.name, func(t *testing.T) {
			var submissionID, jobID int64
			if err := tx.QueryRow(ctx, `INSERT INTO submissions(user_id,problem_id,contest_id,kind,source_artifact_id) VALUES(2,100,100,'contest',101) RETURNING id`).Scan(&submissionID); err != nil {
				t.Fatal(err)
			}
			if err := tx.QueryRow(ctx, `INSERT INTO judge_jobs(submission_id,source_artifact_id,package_artifact_id,package_sha256,manifest,eligible_partitions,execution_id) VALUES($1,101,100,repeat('a',64),$2,ARRAY['snapshot-test'],$3) RETURNING id`, submissionID, test.manifest, test.executionID).Scan(&jobID); err != nil {
				t.Fatal(err)
			}
			// History derives its executor from the Job snapshot, never from the
			// current Problem package (which remains the seeded Slurm package).
			item, err := scanJob(tx.QueryRow(ctx, jobSelect+` WHERE j.id=$1`, jobID))
			if err != nil {
				t.Fatal(err)
			}
			response, err := protoJob(item)
			if err != nil || response.GetExecutionSnapshot().GetExecutor() != test.executor || response.GetExecutionId() != test.executionID {
				t.Fatalf("job execution snapshot = %v, %v", response, err)
			}
			if got := response.GetExecutionSnapshot().GetManifest().AsMap(); got["problemId"] != float64(100) {
				t.Fatalf("manifest snapshot lost: %v", got)
			}
		})
	}
}
