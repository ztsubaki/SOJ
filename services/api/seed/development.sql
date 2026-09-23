-- Local development data. Apply manually after schema.sql:
--   psql "$DATABASE_URL" -f services/api/seed/development.sql
-- Both accounts use password: password.

INSERT INTO users (username, email, password_hash, role, status)
VALUES
  ('admin', 'admin@soj.local', '$argon2id$v=19$m=65536,t=3,p=1$R2CfryxY/dnE6xiH1UQWOg$uf2rTjq9aQROgkQKIjpG+8tTP1o8NOGSR95pOVz7gI8', 'admin', 'active'),
  ('runner', 'runner@soj.local', '$argon2id$v=19$m=65536,t=3,p=1$R2CfryxY/dnE6xiH1UQWOg$uf2rTjq9aQROgkQKIjpG+8tTP1o8NOGSR95pOVz7gI8', 'user', 'active')
ON CONFLICT (username) DO UPDATE SET
  username = EXCLUDED.username, email = EXCLUDED.email, password_hash = EXCLUDED.password_hash,
  role = EXCLUDED.role, status = EXCLUDED.status,
  updated_at = now();

INSERT INTO problems (id, title, content, difficulty, enabled, candidate_partitions, created_by) OVERRIDING SYSTEM VALUE
VALUES
  (1, 'Sum Two Numbers', 'Read two integers and output their sum.', 'Easy', true, ARRAY['local'], (SELECT id FROM users WHERE username = 'admin')),
  (2, 'Hello Grid', 'Print a greeting for the supplied grid.', 'Medium', true, ARRAY['local'], (SELECT id FROM users WHERE username = 'admin'))
ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content,
  difficulty = EXCLUDED.difficulty, enabled = EXCLUDED.enabled,
  candidate_partitions = EXCLUDED.candidate_partitions, updated_at = now();

INSERT INTO contests (id, title, description, starts_at, ends_at, visible, scoring_policy, created_by) OVERRIDING SYSTEM VALUE
VALUES
  (1, 'Local Practice', 'A running local contest for UI and permission testing.', now() - interval '1 hour', now() + interval '7 days', true, 'best', (SELECT id FROM users WHERE username = 'admin')),
  (2, 'Upcoming Local Practice', 'A registration-only local contest.', now() + interval '1 day', now() + interval '8 days', true, 'latest', (SELECT id FROM users WHERE username = 'admin')),
  (3, 'Archived Local Practice', 'An ended contest for public-read testing.', now() - interval '8 days', now() - interval '1 day', true, 'best', (SELECT id FROM users WHERE username = 'admin'))
ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, description = EXCLUDED.description,
  starts_at = EXCLUDED.starts_at, ends_at = EXCLUDED.ends_at, visible = EXCLUDED.visible,
  scoring_policy = EXCLUDED.scoring_policy, updated_at = now();

INSERT INTO contest_problems (contest_id, problem_id, ordinal)
VALUES
  (1, 1, 1),
  (1, 2, 2),
  (2, 1, 1),
  (3, 2, 1)
ON CONFLICT (contest_id, problem_id) DO UPDATE SET ordinal = EXCLUDED.ordinal;

INSERT INTO contest_registrations (contest_id, user_id)
VALUES (1, (SELECT id FROM users WHERE username = 'runner')),
       (2, (SELECT id FROM users WHERE username = 'runner'))
ON CONFLICT DO NOTHING;

SELECT setval(pg_get_serial_sequence('problems', 'id'), (SELECT max(id) FROM problems));
SELECT setval(pg_get_serial_sequence('contests', 'id'), (SELECT max(id) FROM contests));
