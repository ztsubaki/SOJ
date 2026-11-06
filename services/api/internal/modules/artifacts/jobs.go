package artifacts

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// DeleteSubmissionJobs removes jobs selected by an internal submission-ID query.
// Callers must first clear and lock the submissions' current_job_id pointers.
// The returned references still need Release after the submissions are removed;
// object keys may only be removed after the surrounding transaction commits.
func DeleteSubmissionJobs(ctx context.Context, tx pgx.Tx, submissionQuery string, args ...any) ([]int64, []string, error) {
	rows, err := tx.Query(ctx, `SELECT j.id,j.source_artifact_id,j.package_artifact_id,j.result_artifact_id,j.log_artifact_id
FROM judge_jobs j WHERE j.submission_id IN (`+submissionQuery+`) ORDER BY j.id FOR UPDATE OF j`, args...)
	if err != nil {
		return nil, nil, err
	}
	var jobs, refs []int64
	for rows.Next() {
		var id, source, pkg int64
		var result, log *int64
		if err = rows.Scan(&id, &source, &pkg, &result, &log); err != nil {
			rows.Close()
			return nil, nil, err
		}
		jobs = append(jobs, id)
		refs = append(refs, source, pkg)
		if result != nil {
			refs = append(refs, *result)
		}
		if log != nil {
			refs = append(refs, *log)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(jobs) == 0 {
		return nil, nil, nil
	}

	// Upload publication and insertion both lock the owning job. Holding those
	// locks makes this set final, including uploads never bound by CompleteJob.
	rows, err = tx.Query(ctx, `DELETE FROM artifacts WHERE upload_job_id=ANY($1) AND state='pending' RETURNING object_key`, jobs)
	if err != nil {
		return nil, nil, err
	}
	var keys []string
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return nil, nil, err
		}
		keys = append(keys, key)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, nil, err
	}
	// Available uploads have durable job references counted separately. Detach
	// their upload provenance before deleting jobs; Release handles ownership.
	if _, err = tx.Exec(ctx, `UPDATE artifacts SET upload_job_id=NULL,upload_agent_session_id=NULL WHERE upload_job_id=ANY($1) AND state='available'`, jobs); err != nil {
		return nil, nil, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM judge_jobs WHERE id=ANY($1)`, jobs); err != nil {
		return nil, nil, err
	}
	return refs, keys, nil
}
