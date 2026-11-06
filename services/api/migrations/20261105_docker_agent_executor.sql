-- Manual migration only: stop API and Agent processes before applying.
-- Existing sessions remain Slurm; stored execution IDs and terminal values are preserved.
BEGIN;

ALTER TABLE agent_sessions
    ADD COLUMN executor text NOT NULL DEFAULT 'slurm'
    CONSTRAINT agent_sessions_executor_check CHECK (executor IN ('slurm', 'docker'));

ALTER TABLE judge_jobs RENAME COLUMN slurm_job_id TO execution_id;
ALTER TABLE judge_jobs RENAME COLUMN slurm_terminal_state TO execution_terminal_state;
ALTER TABLE judge_jobs RENAME CONSTRAINT judge_jobs_slurm_terminal_state_check TO judge_jobs_execution_terminal_state_check;

COMMIT;
