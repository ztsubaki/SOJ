package contests

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	ErrForbidden         = errors.New("contest resource is not available")
	ErrAlreadyRegistered = errors.New("user is already registered for contest")
)

func (s *Service) PublicList(ctx context.Context, query, desiredStatus string, page, size int) ([]AdminContest, int, error) {
	where := []string{"c.visible"}
	args := []any{}
	if query != "" {
		args = append(args, "%"+query+"%")
		where = append(where, fmt.Sprintf("c.title ILIKE $%d", len(args)))
	}
	if desiredStatus != "" {
		args = append(args, desiredStatus)
		where = append(where, fmt.Sprintf("%s=$%d", derivedStatusSQL("c"), len(args)))
	}
	clause := strings.Join(where, " AND ")
	var total int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM contests c WHERE "+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, size, (page-1)*size)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT c.id,c.title,c.description,%s,c.starts_at,c.ends_at,c.visible,c.scoring_policy,count(cr.user_id) FROM contests c LEFT JOIN contest_registrations cr ON cr.contest_id=c.id WHERE %s GROUP BY c.id ORDER BY c.starts_at DESC,c.id ASC LIMIT $%d OFFSET $%d`, derivedStatusSQL("c"), clause, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []AdminContest{}
	for rows.Next() {
		item, err := scanContest(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, item)
	}
	return out, total, rows.Err()
}

func (s *Service) PublicGet(ctx context.Context, id int64) (AdminContest, error) {
	item, err := s.publicContest(ctx, id)
	if err != nil {
		return item, err
	}
	if !item.Visible {
		return AdminContest{}, ErrNotFound
	}
	return item, nil
}

// publicContest deliberately omits the admin-only problem list. Public reads
// need contest metadata for authorization and presentation, not every linked
// problem and its tags.
func (s *Service) publicContest(ctx context.Context, id int64) (AdminContest, error) {
	row := s.pool.QueryRow(ctx, fmt.Sprintf(`SELECT c.id,c.title,c.description,%s,c.starts_at,c.ends_at,c.visible,c.scoring_policy,count(cr.user_id)
FROM contests c LEFT JOIN contest_registrations cr ON cr.contest_id=c.id WHERE c.id=$1 GROUP BY c.id`, derivedStatusSQL("c")), id)
	item, err := scanContest(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminContest{}, ErrNotFound
	}
	return item, err
}

func (s *Service) AccessibleProblems(ctx context.Context, contestID int64, userID int64) ([]ProblemSummary, error) {
	c, err := s.PublicGet(ctx, contestID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeProblemAccess(ctx, c, userID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT p.id,p.title,p.difficulty,p.tags,
  p.enabled AND cardinality(p.candidate_partitions)>0 AND EXISTS(SELECT 1 FROM artifacts a WHERE a.id=p.package_artifact_id AND a.kind='problem_package' AND a.state='available')
FROM contest_problems cp JOIN problems p ON p.id=cp.problem_id
WHERE cp.contest_id=$1 AND ($2 OR p.enabled)
GROUP BY p.id,cp.ordinal ORDER BY cp.ordinal`, contestID, c.Status == "ended")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProblemSummary{}
	for rows.Next() {
		var p ProblemSummary
		if err = rows.Scan(&p.ID, &p.Title, &p.Difficulty, &p.Tags, &p.Runnable); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) AccessibleProblem(ctx context.Context, contestID, problemID int64, userID int64) (ProblemSummary, string, error) {
	c, err := s.PublicGet(ctx, contestID)
	if err != nil {
		return ProblemSummary{}, "", err
	}
	if err := s.authorizeProblemAccess(ctx, c, userID); err != nil {
		return ProblemSummary{}, "", err
	}
	var item ProblemSummary
	var content string
	err = s.pool.QueryRow(ctx, `SELECT p.id,p.title,p.difficulty,p.tags,p.content,
  p.enabled AND cardinality(p.candidate_partitions)>0 AND EXISTS(SELECT 1 FROM artifacts a WHERE a.id=p.package_artifact_id AND a.kind='problem_package' AND a.state='available')
FROM contest_problems cp JOIN problems p ON p.id=cp.problem_id
WHERE cp.contest_id=$1 AND p.id=$2 AND ($3 OR p.enabled)`, contestID, problemID, c.Status == "ended").Scan(&item.ID, &item.Title, &item.Difficulty, &item.Tags, &content, &item.Runnable)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProblemSummary{}, "", ErrNotFound
	}
	return item, content, err
}

func (s *Service) authorizeProblemAccess(ctx context.Context, contest AdminContest, userID int64) error {
	if contest.Status == "upcoming" {
		return ErrNotFound
	}
	if contest.Status != "running" {
		return nil
	}
	if userID == 0 {
		return ErrForbidden
	}
	var registered bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM contest_registrations WHERE contest_id=$1 AND user_id=$2)`, contest.ID, userID).Scan(&registered); err != nil {
		return err
	}
	if !registered {
		return ErrForbidden
	}
	return nil
}

func (s *Service) RegisterUser(ctx context.Context, contestID int64, userID int64) (time.Time, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback(ctx)
	var ends time.Time
	var visible bool
	err = tx.QueryRow(ctx, `SELECT ends_at,visible FROM contests WHERE id=$1 FOR UPDATE`, contestID).Scan(&ends, &visible)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	if err != nil {
		return time.Time{}, err
	}
	if !visible || !time.Now().Before(ends) {
		return time.Time{}, ErrForbidden
	}
	var registered time.Time
	err = tx.QueryRow(ctx, `INSERT INTO contest_registrations(contest_id,user_id) VALUES($1,$2) ON CONFLICT (contest_id,user_id) DO NOTHING RETURNING registered_at`, contestID, userID).Scan(&registered)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrAlreadyRegistered
	}
	if err != nil {
		return time.Time{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return time.Time{}, err
	}
	return registered, nil
}

func (s *Service) PublicParticipants(ctx context.Context, contestID int64, userID int64, admin bool, page, size int) ([]Participant, int, error) {
	var registered bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM contest_registrations WHERE contest_id=$1 AND user_id=$2)`, contestID, userID).Scan(&registered)
	if err != nil {
		return nil, 0, err
	}
	if !registered && !admin {
		return nil, 0, ErrForbidden
	}
	var total int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM contest_registrations WHERE contest_id=$1`, contestID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `SELECT u.id,u.username,cr.registered_at FROM contest_registrations cr JOIN users u ON u.id=cr.user_id WHERE cr.contest_id=$1 ORDER BY cr.registered_at ASC,u.id ASC LIMIT $2 OFFSET $3`, contestID, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Participant{}
	for rows.Next() {
		var p Participant
		if err = rows.Scan(&p.UserID, &p.Username, &p.RegisteredAt); err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}

type Participant struct {
	UserID       int64
	Username     string
	RegisteredAt time.Time
}
