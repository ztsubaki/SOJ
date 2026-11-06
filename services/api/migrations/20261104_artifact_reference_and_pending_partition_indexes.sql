-- Apply manually to an existing database during planned maintenance.
-- Ordinary CREATE INDEX takes write-blocking table locks until COMMIT.
-- This migration changes indexes only; it does not change business data.
BEGIN;

CREATE INDEX artifacts_upload_job_idx ON artifacts (upload_job_id);
DROP INDEX artifacts_pending_job_idx;

CREATE INDEX problems_package_artifact_idx ON problems (package_artifact_id);
CREATE INDEX submissions_source_artifact_idx ON submissions (source_artifact_id);
CREATE INDEX submissions_current_job_idx ON submissions (current_job_id);
CREATE INDEX judge_jobs_source_artifact_idx ON judge_jobs (source_artifact_id);
CREATE INDEX judge_jobs_package_artifact_idx ON judge_jobs (package_artifact_id);
CREATE INDEX judge_jobs_result_artifact_idx ON judge_jobs (result_artifact_id);
CREATE INDEX judge_jobs_log_artifact_idx ON judge_jobs (log_artifact_id);
CREATE INDEX judge_jobs_pending_partitions_idx ON judge_jobs USING gin (eligible_partitions) WHERE state = 'pending';

COMMIT;
