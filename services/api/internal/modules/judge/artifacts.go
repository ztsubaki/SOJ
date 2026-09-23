package judge

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

func (s *ControlServer) ArtifactHTTP(w http.ResponseWriter, r *http.Request, job, artifact string) {
	values := r.Header.Values("Authorization")
	sessions := r.Header.Values("X-SOJ-Agent-Session")
	if len(values) != 1 || len(sessions) != 1 || s.serviceToken == "" || subtle.ConstantTimeCompare([]byte(values[0]), []byte("Bearer "+s.serviceToken)) != 1 {
		http.Error(w, "unauthorized", 401)
		return
	}
	agent := sessions[0]
	jobID, err := strconv.ParseInt(job, 10, 64)
	if !validAgentJob(agent, jobID) {
		http.Error(w, "invalid identity", 400)
		return
	}
	upload := r.Method == "POST"
	kind := r.URL.Query().Get("kind")
	if upload && (kind != "judge_result" && kind != "judge_log" || r.Header.Get("Content-Type") != "application/octet-stream") {
		http.Error(w, "invalid upload", 400)
		return
	}
	check := func() int {
		var owner, state string
		var live bool
		err := s.pool.QueryRow(r.Context(), `SELECT COALESCE(j.agent_session_id,''),j.state,COALESCE(a.offline_at IS NULL AND a.ended_at IS NULL AND a.last_heartbeat_at>now()-$2::interval,false) FROM judge_jobs j LEFT JOIN agent_sessions a ON a.id=j.agent_session_id WHERE j.id=$1`, jobID, OfflineThreshold.String()).Scan(&owner, &state, &live)
		if err != nil {
			return 404
		}
		if owner != agent {
			return 403
		}
		if !live || !activeState(state) || upload && state != "finishing" {
			return 409
		}
		return 0
	}
	if code := check(); code != 0 {
		http.Error(w, http.StatusText(code), code)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if !upload {
		artifactID, parseErr := strconv.ParseInt(artifact, 10, 64)
		if r.Method != "GET" || parseErr != nil || artifactID <= 0 {
			http.Error(w, "invalid artifact request", 400)
			return
		}
		var key, hash string
		var size int64
		err := s.pool.QueryRow(r.Context(), `SELECT a.object_key,a.sha256,a.size_bytes FROM artifacts a JOIN judge_jobs j ON j.id=$1 WHERE a.id=$2 AND a.state='available' AND ((a.id=j.source_artifact_id AND a.kind='submission_source') OR (a.id=j.package_artifact_id AND a.kind='problem_package'))`, jobID, artifactID).Scan(&key, &hash, &size)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(filepath.Join(s.artifactRoot, filepath.Base(key)))
		if err != nil {
			http.Error(w, "artifact unavailable", 503)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		w.Header().Set("X-SOJ-SHA256", hash)
		io.Copy(w, f)
		return
	}
	limit := s.maxArtifactSize
	if kind == "judge_result" && limit > 1<<20 {
		limit = 1 << 20
	}
	if r.ContentLength > limit {
		http.Error(w, "artifact too large", 413)
		return
	}
	if err := os.MkdirAll(s.artifactRoot, 0700); err != nil {
		http.Error(w, "storage unavailable", 503)
		return
	}
	f, err := os.CreateTemp(s.artifactRoot, ".upload-*")
	if err != nil {
		http.Error(w, "storage unavailable", 503)
		return
	}
	path := f.Name()
	keep := false
	defer func() {
		if !keep {
			os.Remove(path)
		}
	}()
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(f, hash), io.LimitReader(r.Body, limit+1))
	closeErr := f.Close()
	if size > limit {
		http.Error(w, "artifact too large", 413)
		return
	}
	if copyErr != nil || closeErr != nil || size == 0 || r.ContentLength >= 0 && size != r.ContentLength {
		http.Error(w, "incomplete upload", 400)
		return
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	defer tx.Rollback(r.Context())
	v, j, err := lockOwned(r.Context(), tx, agent, jobID)
	if err != nil || !v.live() || j.state != "finishing" {
		http.Error(w, "job unavailable", 409)
		return
	}
	var id int64
	if err = tx.QueryRow(r.Context(), `SELECT nextval(pg_get_serial_sequence('artifacts','id'))`).Scan(&id); err != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	key := strconv.FormatInt(id, 10)
	final := filepath.Join(s.artifactRoot, key)
	if err = os.Rename(path, final); err != nil {
		http.Error(w, "storage unavailable", 503)
		return
	}
	path = final
	digest := hex.EncodeToString(hash.Sum(nil))
	contentType := "text/plain; charset=utf-8"
	if kind == "judge_result" {
		contentType = "application/json; charset=utf-8"
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO artifacts(id,object_key,kind,state,size_bytes,sha256,content_type,upload_agent_session_id,upload_job_id) OVERRIDING SYSTEM VALUE VALUES($1,$2,$3,'pending',$4,$5,$6,$7,$8)`, id, key, kind, size, digest, contentType, agent, jobID)
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	keep = true
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	json.NewEncoder(w).Encode(map[string]any{"artifactId": id, "sha256": digest, "sizeBytes": size})
}
