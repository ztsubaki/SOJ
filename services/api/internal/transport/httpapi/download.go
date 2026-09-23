package httpapi

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/ztsubaki/soj/services/api/internal/modules/submissions"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func downloadHandler(cfg Config, admin bool, kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := cfg.WebBFFToken
		if admin {
			token = cfg.AdminBFFToken
		}
		user, ok := internalBFFUser(c, token, cfg.Auth, admin)
		if !ok {
			return
		}
		submissionID, err := strconv.ParseInt(c.Param("submissionId"), 10, 64)
		jobID, jobErr := strconv.ParseInt(c.Param("jobId"), 10, 64)
		if submissionID <= 0 || err != nil || c.Param("jobId") != "" && (jobErr != nil || jobID <= 0) {
			c.JSON(400, gin.H{"code": 4001, "error": "invalid resource id"})
			return
		}
		var meta submissions.ArtifactMetadata
		var metaErr error
		if !admin {
			meta, metaErr = cfg.Submissions.CurrentLogMetadata(c, submissionID, user.ID, false)
		} else if kind == "source" {
			meta, metaErr = cfg.Submissions.SourceMetadata(c, submissionID, user.ID, true)
		} else {
			meta, metaErr = cfg.Submissions.JobArtifactMetadata(c, submissionID, jobID, kind)
		}
		if metaErr != nil {
			c.JSON(404, gin.H{"code": 4040, "error": "artifact not found"})
			return
		}
		var key string
		if err = cfg.Pool.QueryRow(c, `SELECT object_key FROM artifacts WHERE id=$1 AND state='available'`, meta.ID).Scan(&key); err != nil {
			c.JSON(404, gin.H{"code": 4040, "error": "artifact not found"})
			return
		}
		info, err := os.Stat(filepath.Join(cfg.ArtifactRoot, filepath.Base(key)))
		if err != nil || uint64(info.Size()) != meta.SizeBytes {
			c.JSON(503, gin.H{"code": 5030, "error": "artifact unavailable"})
			return
		}
		content, _, err := mime.ParseMediaType(meta.ContentType)
		if err != nil {
			content = "application/octet-stream"
		}
		filename := strings.Map(func(r rune) rune {
			if r < 32 || r == 127 || r == '/' || r == '\\' {
				return -1
			}
			return r
		}, meta.Filename)
		if filename == "" {
			filename = "download"
		}
		c.Header("Cache-Control", "private, no-store")
		c.Header("Content-Type", content)
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Content-Length", fmt.Sprint(meta.SizeBytes))
		c.Header("X-SOJ-SHA256", meta.SHA256)
		c.Header("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(filename))
		if c.Request.Method == http.MethodHead {
			c.Status(200)
			return
		}
		file, err := os.Open(filepath.Join(cfg.ArtifactRoot, filepath.Base(key)))
		if err != nil {
			c.Status(503)
			return
		}
		defer file.Close()
		if _, err = io.Copy(c.Writer, file); err != nil {
			c.Abort()
		}
	}
}
