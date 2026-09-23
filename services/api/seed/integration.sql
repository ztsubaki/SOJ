-- Local-only Control Plane integration fixture. Apply manually after schema.sql.
INSERT INTO users (id, username, email, password_hash, role, status) OVERRIDING SYSTEM VALUE
VALUES
  (1, 'admin-integration', 'admin-integration@soj.local', 'not-for-login', 'admin', 'active'),
  (2, 'user-integration', 'user-integration@soj.local', 'not-for-login', 'user', 'active')
ON CONFLICT (id) DO UPDATE SET role = EXCLUDED.role, status = EXCLUDED.status;

INSERT INTO problems (id, title, content, difficulty, enabled, candidate_partitions, created_by) OVERRIDING SYSTEM VALUE
VALUES (100, 'Integration problem', '', 'Easy', true, ARRAY['integration'], 1)
ON CONFLICT (id) DO NOTHING;

INSERT INTO contests (id, title, description, starts_at, ends_at, visible, scoring_policy, created_by) OVERRIDING SYSTEM VALUE
VALUES (100, 'Integration contest', '', now() - interval '1 hour', now() + interval '1 hour', true, 'best', 1)
ON CONFLICT (id) DO NOTHING;
INSERT INTO contest_problems (contest_id, problem_id, ordinal) VALUES (100, 100, 1) ON CONFLICT DO NOTHING;
INSERT INTO contest_registrations (contest_id, user_id) VALUES (100, 2) ON CONFLICT DO NOTHING;

INSERT INTO artifacts (id, object_key, kind, state, size_bytes, sha256, content_type, available_at, ref_count) OVERRIDING SYSTEM VALUE
VALUES
  (100, '100', 'problem_package', 'available', 6, '5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03', 'application/zstd', now(), 2),
  (101, '101', 'submission_source', 'available', 6, '5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03', 'application/octet-stream', now(), 2)
ON CONFLICT (id) DO UPDATE SET object_key = EXCLUDED.object_key, state = EXCLUDED.state, size_bytes = EXCLUDED.size_bytes, sha256 = EXCLUDED.sha256, available_at = EXCLUDED.available_at, ref_count = EXCLUDED.ref_count;

UPDATE problems
SET enabled = true,
    candidate_partitions = ARRAY['integration'],
    package_artifact_id = 100,
    package_manifest = '{"schemaVersion":1,"problemId":100}'::jsonb
WHERE id = 100;

INSERT INTO submissions (id, user_id, problem_id, contest_id, kind, source_artifact_id) OVERRIDING SYSTEM VALUE
VALUES (100, 2, 100, 100, 'contest', 101)
ON CONFLICT (id) DO NOTHING;

INSERT INTO judge_jobs (id, submission_id, source_artifact_id, package_artifact_id, package_sha256, manifest, eligible_partitions) OVERRIDING SYSTEM VALUE
VALUES (100, 100, 101, 100, '5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03',
  '{"schemaVersion":1,"problemId":100}'::jsonb,
  ARRAY['integration'])
ON CONFLICT (id) DO NOTHING;

UPDATE submissions SET current_job_id = 100 WHERE id = 100;

SELECT setval(pg_get_serial_sequence('problems', 'id'), (SELECT max(id) FROM problems));
SELECT setval(pg_get_serial_sequence('contests', 'id'), (SELECT max(id) FROM contests));
SELECT setval(pg_get_serial_sequence('users', 'id'), (SELECT max(id) FROM users));
SELECT setval(pg_get_serial_sequence('artifacts', 'id'), (SELECT max(id) FROM artifacts));
SELECT setval(pg_get_serial_sequence('submissions', 'id'), (SELECT max(id) FROM submissions));
SELECT setval(pg_get_serial_sequence('judge_jobs', 'id'), (SELECT max(id) FROM judge_jobs));
