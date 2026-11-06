-- SOJ Control Plane PostgreSQL schema. Apply to an empty database.

BEGIN;

CREATE FUNCTION soj_text_array_is_distinct(input_values text[])
RETURNS boolean
LANGUAGE sql
IMMUTABLE
STRICT
AS $$
    SELECT count(*) = count(DISTINCT item.value)
    FROM unnest(input_values) AS item(value);
$$;

CREATE TABLE users (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username text NOT NULL,
    email text NOT NULL,
    password_hash text NOT NULL,
    role text NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin')),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_username_format CHECK (username ~ '^[a-z0-9_-]{3,32}$'),
    CONSTRAINT users_email_format CHECK (email = lower(email) AND length(email) BETWEEN 1 AND 254)
);
CREATE UNIQUE INDEX users_username_unique ON users (username);
CREATE UNIQUE INDEX users_email_unique ON users (email);

CREATE TABLE sessions (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id),
    token_hash text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz
);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

CREATE TABLE artifacts (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    object_key text NOT NULL UNIQUE,
    kind text NOT NULL CHECK (kind IN ('submission_source', 'problem_package', 'judge_result', 'judge_log')),
    state text NOT NULL CHECK (state IN ('pending', 'available')),
    size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
    sha256 text NOT NULL,
    content_type text NOT NULL,
    ref_count bigint NOT NULL DEFAULT 0 CHECK (ref_count >= 0),
    upload_agent_session_id text,
    upload_job_id bigint,
    available_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT artifacts_metadata CHECK (
        sha256 ~ '^[0-9a-f]{64}$' AND (
            (state = 'pending' AND available_at IS NULL AND upload_job_id IS NOT NULL AND upload_agent_session_id IS NOT NULL) OR
            (state = 'available' AND available_at IS NOT NULL)
        )
    )
);
CREATE INDEX artifacts_pending_created_idx ON artifacts (created_at) WHERE state = 'pending';
CREATE INDEX artifacts_upload_job_idx ON artifacts (upload_job_id);

CREATE TABLE problems (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    title text NOT NULL,
    content text NOT NULL,
    difficulty text NOT NULL CHECK (difficulty IN ('Easy', 'Medium', 'Hard')),
    enabled boolean NOT NULL DEFAULT true,
    candidate_partitions text[] NOT NULL,
    tags text[] NOT NULL DEFAULT '{}',
    package_artifact_id bigint REFERENCES artifacts(id) ON DELETE RESTRICT,
    package_manifest jsonb,
    created_by bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT problems_partitions_nonempty CHECK (cardinality(candidate_partitions) > 0),
    CONSTRAINT problems_partitions_distinct CHECK (soj_text_array_is_distinct(candidate_partitions))
);
CREATE INDEX problems_enabled_idx ON problems (enabled, created_at DESC, id ASC);
CREATE INDEX problems_package_artifact_idx ON problems (package_artifact_id);

CREATE TABLE contests (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    title text NOT NULL,
    description text NOT NULL,
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    visible boolean NOT NULL DEFAULT false,
    scoring_policy text NOT NULL DEFAULT 'best' CHECK (scoring_policy IN ('best', 'latest')),
    created_by bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT contests_time_order CHECK (starts_at < ends_at)
);
CREATE INDEX contests_starts_at_idx ON contests (starts_at DESC, id ASC);

CREATE TABLE contest_problems (
    contest_id bigint NOT NULL REFERENCES contests(id) ON DELETE RESTRICT,
    problem_id bigint NOT NULL REFERENCES problems(id) ON DELETE RESTRICT,
    ordinal integer NOT NULL CHECK (ordinal > 0),
    PRIMARY KEY (contest_id, problem_id),
    UNIQUE (contest_id, ordinal)
);
CREATE INDEX contest_problems_problem_id_idx ON contest_problems (problem_id, contest_id);

CREATE TABLE contest_registrations (
    contest_id bigint NOT NULL REFERENCES contests(id) ON DELETE RESTRICT,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    registered_at timestamptz NOT NULL DEFAULT now(),
    disqualified_at timestamptz,
    disqualified_reason text,
    PRIMARY KEY (contest_id, user_id),
    CONSTRAINT contest_registrations_disqualification CHECK (
        (disqualified_at IS NULL AND disqualified_reason IS NULL) OR
        (disqualified_at IS NOT NULL AND disqualified_reason IS NOT NULL)
    )
);
CREATE INDEX contest_registrations_user_idx ON contest_registrations (user_id, registered_at ASC, contest_id ASC);

CREATE TABLE submissions (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    problem_id bigint NOT NULL REFERENCES problems(id) ON DELETE RESTRICT,
    contest_id bigint REFERENCES contests(id) ON DELETE RESTRICT,
    kind text NOT NULL CHECK (kind IN ('contest', 'admin_test')),
    source_artifact_id bigint NOT NULL REFERENCES artifacts(id) ON DELETE RESTRICT,
    source_filename text NOT NULL DEFAULT 'submission',
    source_content_type text NOT NULL DEFAULT 'application/octet-stream',
    current_job_id bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT submissions_contest_shape CHECK (
        (kind = 'contest' AND contest_id IS NOT NULL) OR
        (kind = 'admin_test' AND contest_id IS NULL)
    )
);
CREATE INDEX submissions_user_created_idx ON submissions (user_id, created_at DESC, id ASC);
CREATE INDEX submissions_contest_user_idx ON submissions (contest_id, user_id, created_at DESC, id DESC) WHERE kind = 'contest';
CREATE INDEX submissions_problem_created_idx ON submissions (problem_id, created_at DESC, id ASC);
CREATE INDEX submissions_source_artifact_idx ON submissions (source_artifact_id);
CREATE INDEX submissions_current_job_idx ON submissions (current_job_id);

CREATE TABLE agent_sessions (
    id text PRIMARY KEY,
    partition text NOT NULL,
    executor text NOT NULL DEFAULT 'slurm' CHECK (executor IN ('slurm', 'docker')),
    registered_at timestamptz NOT NULL DEFAULT now(),
    last_heartbeat_at timestamptz NOT NULL DEFAULT now(),
    drain_requested_at timestamptz,
    offline_at timestamptz,
    ended_at timestamptz,
    CONSTRAINT agent_sessions_partition_ascii CHECK (partition ~ '^[!-~]{1,255}$')
);
CREATE UNIQUE INDEX agent_sessions_partition_active_unique ON agent_sessions (partition) WHERE offline_at IS NULL AND ended_at IS NULL;
CREATE INDEX agent_sessions_partition_heartbeat_idx ON agent_sessions (partition, last_heartbeat_at DESC);

ALTER TABLE artifacts
    ADD CONSTRAINT artifacts_upload_agent_session_fk
    FOREIGN KEY (upload_agent_session_id) REFERENCES agent_sessions(id) ON DELETE RESTRICT;

CREATE TABLE judge_jobs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    submission_id bigint NOT NULL REFERENCES submissions(id) ON DELETE RESTRICT,
    source_artifact_id bigint NOT NULL REFERENCES artifacts(id) ON DELETE RESTRICT,
    package_artifact_id bigint NOT NULL REFERENCES artifacts(id) ON DELETE RESTRICT,
    package_sha256 text NOT NULL CHECK (package_sha256 ~ '^[0-9a-f]{64}$'),
    manifest jsonb NOT NULL,
    eligible_partitions text[] NOT NULL,
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'claimed', 'submitted', 'running', 'finishing', 'completed', 'cancelled', 'internal_error')),
    agent_session_id text REFERENCES agent_sessions(id) ON DELETE RESTRICT,
    assigned_partition text,
    execution_id text,
    execution_terminal_state text CHECK (execution_terminal_state IN ('COMPLETED', 'FAILED', 'TIMEOUT', 'OUT_OF_MEMORY', 'CANCELLED')),
    result_artifact_id bigint REFERENCES artifacts(id) ON DELETE RESTRICT,
    log_artifact_id bigint REFERENCES artifacts(id) ON DELETE RESTRICT,
    verdict text CHECK (verdict IN ('accepted', 'wrong_answer', 'compile_error', 'runtime_error', 'time_limit', 'memory_limit')),
    score numeric,
    message text,
    cancel_requested_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    claimed_at timestamptz,
    submitted_at timestamptz,
    started_at timestamptz,
    finishing_at timestamptz,
    finished_at timestamptz,
    CONSTRAINT judge_jobs_partitions_nonempty CHECK (cardinality(eligible_partitions) > 0),
    CONSTRAINT judge_jobs_assignment_shape CHECK (
        (state IN ('pending', 'cancelled') AND agent_session_id IS NULL AND assigned_partition IS NULL) OR
        (state <> 'pending' AND agent_session_id IS NOT NULL AND assigned_partition IS NOT NULL)
    ),
    CONSTRAINT judge_jobs_score_nonnegative CHECK (score IS NULL OR score >= 0)
);
CREATE INDEX judge_jobs_claim_idx ON judge_jobs (created_at ASC, id ASC) WHERE state = 'pending';
CREATE INDEX judge_jobs_pending_partitions_idx ON judge_jobs USING gin (eligible_partitions) WHERE state = 'pending';
CREATE INDEX judge_jobs_source_artifact_idx ON judge_jobs (source_artifact_id);
CREATE INDEX judge_jobs_package_artifact_idx ON judge_jobs (package_artifact_id);
CREATE INDEX judge_jobs_result_artifact_idx ON judge_jobs (result_artifact_id);
CREATE INDEX judge_jobs_log_artifact_idx ON judge_jobs (log_artifact_id);
CREATE INDEX judge_jobs_session_state_idx ON judge_jobs (agent_session_id, state) WHERE state IN ('claimed', 'submitted', 'running', 'finishing');
CREATE INDEX judge_jobs_submission_created_idx ON judge_jobs (submission_id, created_at ASC, id ASC);
CREATE UNIQUE INDEX judge_jobs_session_waiting_unique ON judge_jobs (agent_session_id) WHERE state IN ('claimed', 'submitted');

CREATE FUNCTION soj_job_updated_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at = clock_timestamp();
    RETURN NEW;
END;
$$;
CREATE TRIGGER judge_jobs_updated_at BEFORE UPDATE ON judge_jobs FOR EACH ROW EXECUTE FUNCTION soj_job_updated_at();

ALTER TABLE submissions
    ADD CONSTRAINT submissions_current_job_fk
    FOREIGN KEY (current_job_id) REFERENCES judge_jobs(id) ON DELETE RESTRICT;

ALTER TABLE artifacts
    ADD CONSTRAINT artifacts_upload_job_fk
    FOREIGN KEY (upload_job_id) REFERENCES judge_jobs(id) ON DELETE RESTRICT;

COMMIT;
