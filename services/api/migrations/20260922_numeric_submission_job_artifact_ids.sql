-- Planned-maintenance migration from legacy opaque text IDs to bigint IDs.
-- Stop Control Plane and drain Judge Agents before applying this migration.
BEGIN;

CREATE TABLE _soj_artifact_id_map (old_id text PRIMARY KEY, new_id bigint GENERATED ALWAYS AS IDENTITY UNIQUE);
INSERT INTO _soj_artifact_id_map(old_id) SELECT id FROM artifacts ORDER BY created_at, id;
CREATE TABLE _soj_submission_id_map (old_id text PRIMARY KEY, new_id bigint GENERATED ALWAYS AS IDENTITY UNIQUE);
INSERT INTO _soj_submission_id_map(old_id) SELECT id FROM submissions ORDER BY created_at, id;
CREATE TABLE _soj_job_id_map (old_id text PRIMARY KEY, new_id bigint GENERATED ALWAYS AS IDENTITY UNIQUE);
INSERT INTO _soj_job_id_map(old_id) SELECT id FROM judge_jobs ORDER BY created_at, id;

ALTER TABLE artifacts ADD COLUMN id_num bigint, ADD COLUMN upload_job_id_num bigint;
ALTER TABLE problems ADD COLUMN package_artifact_id_num bigint;
ALTER TABLE submissions ADD COLUMN id_num bigint, ADD COLUMN source_artifact_id_num bigint, ADD COLUMN current_job_id_num bigint;
ALTER TABLE judge_jobs ADD COLUMN id_num bigint, ADD COLUMN submission_id_num bigint, ADD COLUMN source_artifact_id_num bigint, ADD COLUMN package_artifact_id_num bigint, ADD COLUMN result_artifact_id_num bigint, ADD COLUMN log_artifact_id_num bigint;

UPDATE artifacts a SET id_num=m.new_id FROM _soj_artifact_id_map m WHERE m.old_id=a.id;
UPDATE artifacts a SET upload_job_id_num=m.new_id FROM _soj_job_id_map m WHERE m.old_id=a.upload_job_id;
UPDATE problems p SET package_artifact_id_num=m.new_id FROM _soj_artifact_id_map m WHERE m.old_id=p.package_artifact_id;
UPDATE submissions s SET id_num=m.new_id FROM _soj_submission_id_map m WHERE m.old_id=s.id;
UPDATE submissions s SET source_artifact_id_num=m.new_id FROM _soj_artifact_id_map m WHERE m.old_id=s.source_artifact_id;
UPDATE submissions s SET current_job_id_num=m.new_id FROM _soj_job_id_map m WHERE m.old_id=s.current_job_id;
UPDATE judge_jobs j SET id_num=m.new_id FROM _soj_job_id_map m WHERE m.old_id=j.id;
UPDATE judge_jobs j SET submission_id_num=m.new_id FROM _soj_submission_id_map m WHERE m.old_id=j.submission_id;
UPDATE judge_jobs j SET source_artifact_id_num=m.new_id FROM _soj_artifact_id_map m WHERE m.old_id=j.source_artifact_id;
UPDATE judge_jobs j SET package_artifact_id_num=m.new_id FROM _soj_artifact_id_map m WHERE m.old_id=j.package_artifact_id;
UPDATE judge_jobs j SET result_artifact_id_num=m.new_id FROM _soj_artifact_id_map m WHERE m.old_id=j.result_artifact_id;
UPDATE judge_jobs j SET log_artifact_id_num=m.new_id FROM _soj_artifact_id_map m WHERE m.old_id=j.log_artifact_id;

ALTER TABLE artifacts DROP CONSTRAINT artifacts_upload_job_fk, DROP CONSTRAINT artifacts_id_format;
ALTER TABLE problems DROP CONSTRAINT problems_package_artifact_id_fkey;
ALTER TABLE submissions DROP CONSTRAINT submissions_current_job_fk, DROP CONSTRAINT submissions_source_artifact_id_fkey, DROP CONSTRAINT submissions_id_format;
ALTER TABLE judge_jobs DROP CONSTRAINT judge_jobs_submission_id_fkey, DROP CONSTRAINT judge_jobs_source_artifact_id_fkey, DROP CONSTRAINT judge_jobs_package_artifact_id_fkey, DROP CONSTRAINT judge_jobs_result_artifact_id_fkey, DROP CONSTRAINT judge_jobs_log_artifact_id_fkey, DROP CONSTRAINT judge_jobs_id_format;
ALTER TABLE artifacts DROP CONSTRAINT artifacts_pkey;
ALTER TABLE submissions DROP CONSTRAINT submissions_pkey;
ALTER TABLE judge_jobs DROP CONSTRAINT judge_jobs_pkey;

ALTER TABLE artifacts DROP COLUMN id, DROP COLUMN upload_job_id;
ALTER TABLE problems DROP COLUMN package_artifact_id;
ALTER TABLE submissions DROP COLUMN id, DROP COLUMN source_artifact_id, DROP COLUMN current_job_id;
ALTER TABLE judge_jobs DROP COLUMN id, DROP COLUMN submission_id, DROP COLUMN source_artifact_id, DROP COLUMN package_artifact_id, DROP COLUMN result_artifact_id, DROP COLUMN log_artifact_id;

ALTER TABLE artifacts RENAME COLUMN id_num TO id;
ALTER TABLE artifacts RENAME COLUMN upload_job_id_num TO upload_job_id;
ALTER TABLE problems RENAME COLUMN package_artifact_id_num TO package_artifact_id;
ALTER TABLE submissions RENAME COLUMN id_num TO id;
ALTER TABLE submissions RENAME COLUMN source_artifact_id_num TO source_artifact_id;
ALTER TABLE submissions RENAME COLUMN current_job_id_num TO current_job_id;
ALTER TABLE judge_jobs RENAME COLUMN id_num TO id;
ALTER TABLE judge_jobs RENAME COLUMN submission_id_num TO submission_id;
ALTER TABLE judge_jobs RENAME COLUMN source_artifact_id_num TO source_artifact_id;
ALTER TABLE judge_jobs RENAME COLUMN package_artifact_id_num TO package_artifact_id;
ALTER TABLE judge_jobs RENAME COLUMN result_artifact_id_num TO result_artifact_id;
ALTER TABLE judge_jobs RENAME COLUMN log_artifact_id_num TO log_artifact_id;

ALTER TABLE artifacts ALTER COLUMN id SET NOT NULL, ADD PRIMARY KEY (id), ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY;
ALTER TABLE submissions ALTER COLUMN id SET NOT NULL, ADD PRIMARY KEY (id), ALTER COLUMN source_artifact_id SET NOT NULL, ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY;
ALTER TABLE judge_jobs ALTER COLUMN id SET NOT NULL, ADD PRIMARY KEY (id), ALTER COLUMN submission_id SET NOT NULL, ALTER COLUMN source_artifact_id SET NOT NULL, ALTER COLUMN package_artifact_id SET NOT NULL, ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY;
ALTER TABLE problems ADD CONSTRAINT problems_package_artifact_id_fkey FOREIGN KEY (package_artifact_id) REFERENCES artifacts(id) ON DELETE RESTRICT;
ALTER TABLE submissions ADD CONSTRAINT submissions_source_artifact_id_fkey FOREIGN KEY (source_artifact_id) REFERENCES artifacts(id) ON DELETE RESTRICT, ADD CONSTRAINT submissions_current_job_fk FOREIGN KEY (current_job_id) REFERENCES judge_jobs(id) ON DELETE RESTRICT;
ALTER TABLE judge_jobs ADD CONSTRAINT judge_jobs_submission_id_fkey FOREIGN KEY (submission_id) REFERENCES submissions(id) ON DELETE RESTRICT, ADD CONSTRAINT judge_jobs_source_artifact_id_fkey FOREIGN KEY (source_artifact_id) REFERENCES artifacts(id) ON DELETE RESTRICT, ADD CONSTRAINT judge_jobs_package_artifact_id_fkey FOREIGN KEY (package_artifact_id) REFERENCES artifacts(id) ON DELETE RESTRICT, ADD CONSTRAINT judge_jobs_result_artifact_id_fkey FOREIGN KEY (result_artifact_id) REFERENCES artifacts(id) ON DELETE RESTRICT, ADD CONSTRAINT judge_jobs_log_artifact_id_fkey FOREIGN KEY (log_artifact_id) REFERENCES artifacts(id) ON DELETE RESTRICT;
ALTER TABLE artifacts ADD CONSTRAINT artifacts_upload_job_fk FOREIGN KEY (upload_job_id) REFERENCES judge_jobs(id) ON DELETE RESTRICT;

CREATE INDEX artifacts_pending_job_idx ON artifacts (upload_job_id) WHERE state = 'pending';
CREATE INDEX submissions_user_created_idx ON submissions (user_id, created_at DESC, id ASC);
CREATE INDEX submissions_contest_user_idx ON submissions (contest_id, user_id, created_at DESC, id DESC) WHERE kind = 'contest';
CREATE INDEX submissions_problem_created_idx ON submissions (problem_id, created_at DESC, id ASC);
CREATE INDEX judge_jobs_claim_idx ON judge_jobs (created_at ASC, id ASC) WHERE state = 'pending';
CREATE INDEX judge_jobs_submission_created_idx ON judge_jobs (submission_id, created_at ASC, id ASC);

SELECT setval(pg_get_serial_sequence('artifacts','id'), COALESCE((SELECT max(id) FROM artifacts), 1), (SELECT count(*) > 0 FROM artifacts));
SELECT setval(pg_get_serial_sequence('submissions','id'), COALESCE((SELECT max(id) FROM submissions), 1), (SELECT count(*) > 0 FROM submissions));
SELECT setval(pg_get_serial_sequence('judge_jobs','id'), COALESCE((SELECT max(id) FROM judge_jobs), 1), (SELECT count(*) > 0 FROM judge_jobs));
DROP TABLE _soj_artifact_id_map, _soj_submission_id_map, _soj_job_id_map;
COMMIT;
