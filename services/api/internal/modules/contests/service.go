package contests

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"path/filepath"
)

var (
	ErrNotFound               = errors.New("contest not found")
	ErrInvalidContestInput    = errors.New("invalid contest input")
	ErrContestProblemNotFound = errors.New("contest problem not found")
	ErrPublishedProblemReused = errors.New("published problem cannot be reused")
)

type Service struct {
	pool         *pgxpool.Pool
	artifactRoot string
}

func NewService(pool *pgxpool.Pool, artifactRoot string) *Service {
	if pool == nil {
		return nil
	}
	return &Service{pool: pool, artifactRoot: filepath.Clean(artifactRoot)}
}

type LeaderboardEntry struct {
	Rank           int
	UserID         int64
	Username       string
	Score          string
	ProblemScores  []LeaderboardProblemScore
	Solved         int
	Submissions    int
	Disqualified   bool
	LastSubmission *time.Time
}

type LeaderboardProblemScore struct {
	ProblemID int64
	Score     string
}

// Leaderboard reads only current jobs of contest submissions for problems that
// still belong to the contest. The scoring-policy branch deliberately keeps
// incomplete latest submissions: they contribute zero instead of falling back
// to an older completed submission.
func (s *Service) Leaderboard(ctx context.Context, contestID int64, includeDisqualified bool, page, size int) ([]LeaderboardEntry, int, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM contests WHERE id=$1)`, contestID).Scan(&exists); err != nil {
		return nil, 0, err
	}
	if !exists {
		return nil, 0, ErrNotFound
	}
	const base = `WITH contest AS (
  SELECT id,scoring_policy FROM contests WHERE id=$1
), valid_submissions AS (
  SELECT s.id,s.user_id,s.problem_id,s.created_at,j.state,j.score,j.verdict
  FROM submissions s
  JOIN contest_problems cp ON cp.contest_id=s.contest_id AND cp.problem_id=s.problem_id
  LEFT JOIN judge_jobs j ON j.id=s.current_job_id
  WHERE s.contest_id=$1 AND s.kind='contest'
), submission_stats AS (
  SELECT user_id,
    COUNT(*)::int AS submissions,
    COUNT(DISTINCT problem_id) FILTER (WHERE verdict='accepted')::int AS solved,
    MAX(created_at) AS last_submission
  FROM valid_submissions GROUP BY user_id
), best_scores AS (
  SELECT user_id,COALESCE(SUM(best_score),0)::numeric AS score FROM (
    SELECT user_id,problem_id,MAX(score) FILTER (WHERE state='completed') AS best_score
    FROM valid_submissions GROUP BY user_id,problem_id
  ) scored GROUP BY user_id
), latest_rows AS (
  SELECT *,ROW_NUMBER() OVER (PARTITION BY user_id,problem_id ORDER BY created_at DESC,id DESC) AS row_number
  FROM valid_submissions
), latest_scores AS (
  SELECT user_id,COALESCE(SUM(CASE WHEN state='completed' THEN COALESCE(score,0) ELSE 0 END),0)::numeric AS score
  FROM latest_rows WHERE row_number=1 GROUP BY user_id
), entries AS (
  SELECT u.id AS user_id,u.username,cr.disqualified_at IS NOT NULL AS disqualified,
    COALESCE(CASE WHEN c.scoring_policy='best' THEN bs.score ELSE ls.score END,0)::numeric AS score,
    COALESCE(ss.solved,0)::int AS solved,COALESCE(ss.submissions,0)::int AS submissions,ss.last_submission
  FROM contest c
  JOIN contest_registrations cr ON cr.contest_id=c.id
  JOIN users u ON u.id=cr.user_id
  LEFT JOIN submission_stats ss ON ss.user_id=u.id
  LEFT JOIN best_scores bs ON bs.user_id=u.id
  LEFT JOIN latest_scores ls ON ls.user_id=u.id
  WHERE ($2::boolean OR cr.disqualified_at IS NULL)
), ranked AS (
  SELECT *,DENSE_RANK() OVER (ORDER BY score DESC)::int AS rank FROM entries
)
SELECT rank,user_id,username,score::text,solved,submissions,disqualified,last_submission FROM ranked`
	args := []any{contestID, includeDisqualified}
	var total int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM (`+base+`) counted`, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, size, (page-1)*size)
	rows, err := tx.Query(ctx, base+fmt.Sprintf(" ORDER BY score DESC,user_id ASC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []LeaderboardEntry{}
	for rows.Next() {
		var item LeaderboardEntry
		if err = rows.Scan(&item.Rank, &item.UserID, &item.Username, &item.Score, &item.Solved, &item.Submissions, &item.Disqualified, &item.LastSubmission); err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	if err = populateLeaderboardProblemScores(ctx, tx, contestID, items); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// populateLeaderboardProblemScores projects every current contest problem for
// every returned participant. It intentionally mirrors the total-score policy
// so each row's problem scores add up to its total score.
func populateLeaderboardProblemScores(ctx context.Context, tx pgx.Tx, contestID int64, entries []LeaderboardEntry) error {
	if len(entries) == 0 {
		return nil
	}
	userIDs := make([]int64, 0, len(entries))
	byUserID := make(map[int64]int, len(entries))
	for index, entry := range entries {
		userIDs = append(userIDs, entry.UserID)
		byUserID[entry.UserID] = index
	}
	rows, err := tx.Query(ctx, `WITH selected_users AS (
  SELECT unnest($2::bigint[]) AS user_id
), valid_submissions AS (
  SELECT s.id,s.user_id,s.problem_id,s.created_at,j.state,j.score
  FROM submissions s
  JOIN contest_problems cp ON cp.contest_id=s.contest_id AND cp.problem_id=s.problem_id
  LEFT JOIN judge_jobs j ON j.id=s.current_job_id
  WHERE s.contest_id=$1 AND s.kind='contest'
), best_scores AS (
  SELECT user_id,problem_id,MAX(score) FILTER (WHERE state='completed') AS score
  FROM valid_submissions GROUP BY user_id,problem_id
), latest_rows AS (
  SELECT *,ROW_NUMBER() OVER (PARTITION BY user_id,problem_id ORDER BY created_at DESC,id DESC) AS row_number
  FROM valid_submissions
), latest_scores AS (
  SELECT user_id,problem_id,CASE WHEN state='completed' THEN COALESCE(score,0) ELSE 0 END AS score
  FROM latest_rows WHERE row_number=1
)
SELECT su.user_id,cp.problem_id,
  COALESCE(CASE WHEN c.scoring_policy='best' THEN bs.score ELSE ls.score END,0)::numeric::text
FROM selected_users su
CROSS JOIN contest_problems cp
JOIN contests c ON c.id=cp.contest_id
LEFT JOIN best_scores bs ON bs.user_id=su.user_id AND bs.problem_id=cp.problem_id
LEFT JOIN latest_scores ls ON ls.user_id=su.user_id AND ls.problem_id=cp.problem_id
WHERE cp.contest_id=$1
ORDER BY su.user_id,cp.ordinal`, contestID, userIDs)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var userID, problemID int64
		var score string
		if err = rows.Scan(&userID, &problemID, &score); err != nil {
			return err
		}
		entries[byUserID[userID]].ProblemScores = append(entries[byUserID[userID]].ProblemScores, LeaderboardProblemScore{ProblemID: problemID, Score: score})
	}
	return rows.Err()
}
