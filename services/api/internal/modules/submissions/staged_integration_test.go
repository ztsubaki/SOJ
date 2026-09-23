package submissions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCreateStagedSubmissionIntegration(t *testing.T) {
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
	resetStagedSubmissionFixture(t, ctx, pool)
	root := filepath.Join(t.TempDir(), "custom-store")
	service := NewService(pool, root, DefaultMaxSourceSize)
	if err := service.CanCreate(ctx, 1, 9001, 9001); err != nil {
		t.Fatalf("CanCreate(): %v", err)
	}
	staged := writeStagedSource(t, root, "source-ok.c", []byte("int main(void) { return 0; }\n"))
	created, err := service.CreateStaged(ctx, 1, 9001, 9001, staged)
	if err != nil {
		t.Fatalf("CreateStaged(): %v", err)
	}
	if created.CurrentJobID == nil || created.Status != "pending" {
		t.Fatalf("created submission = %#v", created)
	}
	if _, err := os.Stat(staged.Path); !os.IsNotExist(err) {
		t.Fatalf("staged source remains after commit: %v", err)
	}
	var artifactID, jobID int64
	var state string
	if err := pool.QueryRow(ctx, `SELECT s.source_artifact_id,s.current_job_id,a.state FROM submissions s JOIN artifacts a ON a.id=s.source_artifact_id WHERE s.id=$1`, created.ID).Scan(&artifactID, &jobID, &state); err != nil {
		t.Fatal(err)
	}
	if jobID != *created.CurrentJobID || state != "available" {
		t.Fatalf("persisted submission = artifact %d job %d state %q", artifactID, jobID, state)
	}
	if _, err := os.Stat(filepath.Join(root, strconv.FormatInt(artifactID, 10))); err != nil {
		t.Fatalf("durable source object missing: %v", err)
	}

	failed := writeStagedSource(t, root, "source-denied.c", []byte("int main(void) { return 1; }\n"))
	if _, err := pool.Exec(ctx, `UPDATE contests SET visible=false WHERE id=9001`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateStaged(ctx, 1, 9001, 9001, failed); err == nil {
		t.Fatal("CreateStaged() succeeded after the contest was made unavailable")
	}
	if _, err := os.Stat(failed.Path); !os.IsNotExist(err) {
		t.Fatalf("failed staged source remains: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM submissions WHERE contest_id=9001`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("submission count after rejected bind = %d, want 1", count)
	}
}

func TestCreateAdminTestStagedIntegration(t *testing.T) {
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
		`UPDATE submissions SET current_job_id=NULL WHERE problem_id=9002`,
		`DELETE FROM judge_jobs WHERE submission_id IN (SELECT id FROM submissions WHERE problem_id=9002)`,
		`DELETE FROM submissions WHERE problem_id=9002`,
		`INSERT INTO problems(id,title,content,difficulty,enabled,candidate_partitions,package_artifact_id,package_manifest,created_by) OVERRIDING SYSTEM VALUE VALUES (9002,'Admin upload problem','','Easy',true,ARRAY['admin-upload'],100,'{"schemaVersion":1,"problemId":9002}',1) ON CONFLICT (id) DO UPDATE SET enabled=true,candidate_partitions=EXCLUDED.candidate_partitions,package_artifact_id=EXCLUDED.package_artifact_id,package_manifest=EXCLUDED.package_manifest`,
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	root := filepath.Join(t.TempDir(), "custom-store")
	service := NewService(pool, root, DefaultMaxSourceSize)
	if err := service.CanCreateAdminTest(ctx, 9002); err != nil {
		t.Fatalf("CanCreateAdminTest(): %v", err)
	}
	staged := writeStagedSource(t, root, "admin-ok.c", []byte("int main(void) { return 0; }\n"))
	created, err := service.CreateAdminTestStaged(ctx, 1, 9002, staged)
	if err != nil || created.Kind != "admin_test" || created.ContestID != nil || created.CurrentJobID == nil {
		t.Fatalf("CreateAdminTestStaged() = (%#v, %v)", created, err)
	}
	if _, err := os.Stat(staged.Path); !os.IsNotExist(err) {
		t.Fatalf("staged admin source remains after commit: %v", err)
	}

	before, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	unauthorized := writeStagedSource(t, root, "non-admin.c", []byte("int main(void) { return 0; }"))
	if _, err := service.CreateAdminTestStaged(ctx, 2, 9002, unauthorized); err == nil {
		t.Fatal("ordinary user created an admin test")
	}
	after, err := os.ReadDir(root)
	if err != nil || len(after) != len(before) {
		t.Fatalf("rejected admin test left an object: before=%d after=%d error=%v", len(before), len(after), err)
	}
	var submissionCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM submissions WHERE problem_id=9002`).Scan(&submissionCount); err != nil || submissionCount != 1 {
		t.Fatalf("submission count after unauthorized test = %d, error = %v", submissionCount, err)
	}

	failed := writeStagedSource(t, root, "admin-denied.c", []byte("int main(void) { return 1; }\n"))
	if _, err := pool.Exec(ctx, `UPDATE problems SET enabled=false WHERE id=9002`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateAdminTestStaged(ctx, 1, 9002, failed); err == nil {
		t.Fatal("CreateAdminTestStaged() succeeded after the problem was disabled")
	}
	if _, err := os.Stat(failed.Path); !os.IsNotExist(err) {
		t.Fatalf("failed staged admin source remains: %v", err)
	}
}

func writeStagedSource(t *testing.T, root, name string, payload []byte) StagedSource {
	t.Helper()
	dir := root
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".upload-"+name)
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	return StagedSource{Path: path, Filename: name, ContentType: "text/x-c", SHA256: hex.EncodeToString(digest[:]), Size: int64(len(payload))}
}

func resetStagedSubmissionFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	statements := []string{
		`UPDATE submissions SET current_job_id=NULL WHERE contest_id=9001`,
		`DELETE FROM judge_jobs WHERE submission_id IN (SELECT id FROM submissions WHERE contest_id=9001)`,
		`DELETE FROM submissions WHERE contest_id=9001`,
		`DELETE FROM contest_registrations WHERE contest_id=9001`,
		`DELETE FROM contest_problems WHERE contest_id=9001`,
		`DELETE FROM contests WHERE id=9001`,
		`INSERT INTO users(id,username,email,password_hash,role,status) OVERRIDING SYSTEM VALUE VALUES (1,'user-upload','user-upload@soj.local','not-for-login','user','active') ON CONFLICT (id) DO UPDATE SET status='active'`,
		`INSERT INTO problems(id,title,content,difficulty,enabled,candidate_partitions,package_artifact_id,package_manifest,created_by) OVERRIDING SYSTEM VALUE VALUES (9001,'Upload problem','','Easy',true,ARRAY['upload'],100,'{"schemaVersion":1,"problemId":9001}',1) ON CONFLICT (id) DO UPDATE SET enabled=true,candidate_partitions=EXCLUDED.candidate_partitions,package_artifact_id=EXCLUDED.package_artifact_id,package_manifest=EXCLUDED.package_manifest`,
		`INSERT INTO contests(id,title,description,starts_at,ends_at,visible,scoring_policy,created_by) OVERRIDING SYSTEM VALUE VALUES (9001,'Upload contest','',now()-interval '1 hour',now()+interval '1 hour',true,'best',1)`,
		`INSERT INTO contest_problems(contest_id,problem_id,ordinal) VALUES (9001,9001,1)`,
		`INSERT INTO contest_registrations(contest_id,user_id) VALUES (9001,1)`,
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
}
