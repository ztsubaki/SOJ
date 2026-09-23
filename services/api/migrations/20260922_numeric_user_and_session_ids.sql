-- Run during the required planned downtime. This migration replaces the
-- username-derived user primary key and text session primary key with bigint
-- identities. Existing browser sessions are deliberately revoked because the
-- new Cookie is an independent UUID, not session_id.random_secret.

BEGIN;

ALTER TABLE sessions DROP CONSTRAINT sessions_user_id_fkey;
ALTER TABLE problems DROP CONSTRAINT problems_created_by_fkey;
ALTER TABLE contests DROP CONSTRAINT contests_created_by_fkey;
ALTER TABLE contest_registrations DROP CONSTRAINT contest_registrations_user_id_fkey;
ALTER TABLE submissions DROP CONSTRAINT submissions_user_id_fkey;

-- These indexes still point to the old text columns after the columns are
-- renamed below.  Drop them first so their names can be reused for bigint.
DROP INDEX IF EXISTS sessions_user_id_idx;
DROP INDEX IF EXISTS contest_registrations_user_idx;
DROP INDEX IF EXISTS submissions_user_created_idx;
DROP INDEX IF EXISTS submissions_contest_user_idx;

ALTER TABLE users DROP CONSTRAINT users_pkey;
ALTER TABLE users DROP CONSTRAINT users_id_format;
ALTER TABLE users RENAME COLUMN id TO legacy_id;
ALTER TABLE users ADD COLUMN id bigint GENERATED ALWAYS AS IDENTITY;
UPDATE users SET id = DEFAULT;
ALTER TABLE users ALTER COLUMN id SET NOT NULL;
ALTER TABLE users ADD CONSTRAINT users_pkey PRIMARY KEY (id);
ALTER TABLE users ADD CONSTRAINT users_legacy_id_unique UNIQUE (legacy_id);

ALTER TABLE sessions DROP CONSTRAINT sessions_pkey;
ALTER TABLE sessions RENAME COLUMN id TO legacy_id;
ALTER TABLE sessions RENAME COLUMN user_id TO legacy_user_id;
ALTER TABLE sessions ADD COLUMN id bigint GENERATED ALWAYS AS IDENTITY;
ALTER TABLE sessions ADD COLUMN user_id bigint;
UPDATE sessions s SET user_id = u.id FROM users u WHERE u.legacy_id = s.legacy_user_id;
ALTER TABLE sessions ALTER COLUMN id SET NOT NULL;
ALTER TABLE sessions ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE sessions ADD CONSTRAINT sessions_pkey PRIMARY KEY (id);
ALTER TABLE sessions ADD CONSTRAINT sessions_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id);
ALTER TABLE sessions ADD CONSTRAINT sessions_token_hash_key UNIQUE (token_hash);
UPDATE sessions SET revoked_at = COALESCE(revoked_at, now());
ALTER TABLE sessions DROP COLUMN legacy_id;
ALTER TABLE sessions DROP COLUMN legacy_user_id;
CREATE INDEX sessions_user_id_idx ON sessions (user_id);

ALTER TABLE problems RENAME COLUMN created_by TO legacy_created_by;
ALTER TABLE problems ADD COLUMN created_by bigint;
UPDATE problems p SET created_by = u.id FROM users u WHERE u.legacy_id = p.legacy_created_by;
ALTER TABLE problems ADD CONSTRAINT problems_created_by_fkey FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE problems DROP COLUMN legacy_created_by;

ALTER TABLE contests RENAME COLUMN created_by TO legacy_created_by;
ALTER TABLE contests ADD COLUMN created_by bigint;
UPDATE contests c SET created_by = u.id FROM users u WHERE u.legacy_id = c.legacy_created_by;
ALTER TABLE contests ADD CONSTRAINT contests_created_by_fkey FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE contests DROP COLUMN legacy_created_by;

ALTER TABLE contest_registrations DROP CONSTRAINT contest_registrations_pkey;
ALTER TABLE contest_registrations RENAME COLUMN user_id TO legacy_user_id;
ALTER TABLE contest_registrations ADD COLUMN user_id bigint;
UPDATE contest_registrations r SET user_id = u.id FROM users u WHERE u.legacy_id = r.legacy_user_id;
ALTER TABLE contest_registrations ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE contest_registrations ADD CONSTRAINT contest_registrations_pkey PRIMARY KEY (contest_id, user_id);
ALTER TABLE contest_registrations ADD CONSTRAINT contest_registrations_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT;
ALTER TABLE contest_registrations DROP COLUMN legacy_user_id;
CREATE INDEX contest_registrations_user_idx ON contest_registrations (user_id, registered_at ASC, contest_id ASC);

ALTER TABLE submissions RENAME COLUMN user_id TO legacy_user_id;
ALTER TABLE submissions ADD COLUMN user_id bigint;
UPDATE submissions s SET user_id = u.id FROM users u WHERE u.legacy_id = s.legacy_user_id;
ALTER TABLE submissions ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE submissions ADD CONSTRAINT submissions_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT;
ALTER TABLE submissions DROP COLUMN legacy_user_id;
CREATE INDEX submissions_user_created_idx ON submissions (user_id, created_at DESC, id ASC);
CREATE INDEX submissions_contest_user_idx ON submissions (contest_id, user_id, created_at DESC, id DESC) WHERE kind = 'contest';

ALTER TABLE users DROP CONSTRAINT users_legacy_id_unique;
ALTER TABLE users DROP COLUMN legacy_id;

COMMIT;
