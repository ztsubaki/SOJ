package contests

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLeaderboardProjectionIntegration(t *testing.T) {
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
	resetProjectionFixture(t, ctx, pool)
	service := NewService(pool, t.TempDir())

	items, total, err := service.Leaderboard(ctx, 9500, false, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("best leaderboard length = (%d, %d), want (2, 2)", total, len(items))
	}
	assertEntry(t, items[0], 9502, "8", 1, 1, 1, "8", "0")
	assertEntry(t, items[1], 9501, "7", 2, 1, 2, "7", "0")

	if _, err := pool.Exec(ctx, `UPDATE contests SET scoring_policy='latest' WHERE id=9500`); err != nil {
		t.Fatal(err)
	}
	items, _, err = service.Leaderboard(ctx, 9500, false, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	// The newest submission of user one is pending. latest must contribute zero
	// rather than falling back to the older completed score.
	assertEntry(t, items[0], 9502, "8", 1, 1, 1, "8", "0")
	assertEntry(t, items[1], 9501, "0", 2, 1, 2, "0", "0")

	if err := service.AdminDisqualify(ctx, 1, 9500, 9502, true); err != nil {
		t.Fatal(err)
	}
	items, total, err = service.Leaderboard(ctx, 9500, false, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || items[0].UserID != 9501 {
		t.Fatalf("public leaderboard after disqualification = total %d, items %#v", total, items)
	}
	items, total, err = service.Leaderboard(ctx, 9500, true, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || !items[0].Disqualified || items[0].UserID != 9502 {
		t.Fatalf("admin leaderboard after disqualification = total %d, items %#v", total, items)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM contest_problems WHERE contest_id=9500`); err != nil {
		t.Fatal(err)
	}
	items, _, err = service.Leaderboard(ctx, 9500, true, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Score != "0" || len(items[0].ProblemScores) != 0 || items[0].Submissions != 0 || items[1].Score != "0" || len(items[1].ProblemScores) != 0 || items[1].Submissions != 0 {
		t.Fatalf("leaderboard after problem removal = %#v", items)
	}
}

func assertEntry(t *testing.T, item LeaderboardEntry, user int64, score string, rank, solved, submissions int, problemScores ...string) {
	t.Helper()
	if item.UserID != user || item.Score != score || item.Rank != rank || item.Solved != solved || item.Submissions != submissions {
		t.Fatalf("entry = %#v, want user=%d score=%q rank=%d solved=%d submissions=%d", item, user, score, rank, solved, submissions)
	}
	if len(item.ProblemScores) != len(problemScores) {
		t.Fatalf("problem scores = %#v, want %d scores", item.ProblemScores, len(problemScores))
	}
	for i, want := range problemScores {
		if item.ProblemScores[i].Score != want {
			t.Fatalf("problem score %d = %q, want %q", i, item.ProblemScores[i].Score, want)
		}
	}
}

func resetProjectionFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	statements := []string{
		`UPDATE submissions SET current_job_id=NULL WHERE contest_id=9500`,
		`DELETE FROM judge_jobs WHERE submission_id IN (SELECT id FROM submissions WHERE contest_id=9500)`,
		`DELETE FROM submissions WHERE contest_id=9500`,
		`DELETE FROM contest_registrations WHERE contest_id=9500`,
		`DELETE FROM contest_problems WHERE contest_id=9500`,
		`DELETE FROM contests WHERE id=9500`,
		`INSERT INTO users(id,username,email,password_hash,role,status) OVERRIDING SYSTEM VALUE VALUES (9501,'projection-user-one','projection-user-one@soj.local','not-for-login','user','active'),(9502,'projection-user-two','projection-user-two@soj.local','not-for-login','user','active') ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO problems(id,title,content,difficulty,enabled,candidate_partitions,created_by) OVERRIDING SYSTEM VALUE VALUES (9501,'Projection problem','','Easy',true,ARRAY['integration'],1),(9502,'Projection problem two','','Easy',true,ARRAY['integration'],1) ON CONFLICT (id) DO NOTHING`,
		`INSERT INTO contests(id,title,description,starts_at,ends_at,visible,scoring_policy,created_by) OVERRIDING SYSTEM VALUE VALUES (9500,'Projection contest','',now()-interval '1 hour',now()+interval '1 hour',true,'best',1)`,
		`INSERT INTO contest_problems(contest_id,problem_id,ordinal) VALUES (9500,9501,1),(9500,9502,2)`,
		`INSERT INTO contest_registrations(contest_id,user_id) VALUES (9500,9501),(9500,9502)`,
		`INSERT INTO agent_sessions(id,partition) VALUES ('agent-projection','projection') ON CONFLICT (id) DO UPDATE SET offline_at=NULL,ended_at=NULL,last_heartbeat_at=now()`,
		`INSERT INTO submissions(id,user_id,problem_id,contest_id,kind,source_artifact_id,created_at) OVERRIDING SYSTEM VALUE VALUES (9501,9501,9501,9500,'contest',101,now()-interval '2 minutes'),(9502,9501,9501,9500,'contest',101,now()-interval '1 minute'),(9503,9502,9501,9500,'contest',101,now())`,
		`INSERT INTO judge_jobs(id,submission_id,source_artifact_id,package_artifact_id,package_sha256,manifest,eligible_partitions,state,agent_session_id,assigned_partition,verdict,score,finished_at) OVERRIDING SYSTEM VALUE VALUES (9501,9501,101,100,'5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03','{}',ARRAY['projection'],'completed','agent-projection','projection','accepted',7,now()-interval '2 minutes'),(9502,9502,101,100,'5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03','{}',ARRAY['projection'],'pending',NULL,NULL,NULL,NULL,NULL),(9503,9503,101,100,'5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03','{}',ARRAY['projection'],'completed','agent-projection','projection','accepted',8,now())`,
		`UPDATE submissions SET current_job_id=CASE id WHEN 9501 THEN 9501 WHEN 9502 THEN 9502 WHEN 9503 THEN 9503 END WHERE contest_id=9500`,
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
}
