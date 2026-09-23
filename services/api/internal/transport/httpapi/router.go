// Package httpapi owns the Control Plane's small internal HTTP data plane.
package httpapi

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ztsubaki/soj/services/api/internal/modules/auth"
	"github.com/ztsubaki/soj/services/api/internal/modules/judge"
	"github.com/ztsubaki/soj/services/api/internal/modules/problems"
	"github.com/ztsubaki/soj/services/api/internal/modules/submissions"
)

type Config struct {
	Pool          *pgxpool.Pool
	ArtifactRoot  string
	Judge         *judge.ControlServer
	WebBFFToken   string
	AdminBFFToken string
	Auth          *auth.Service
	Problems      *problems.Service
	Submissions   *submissions.Service
}

func NewRouter(cfg Config) *gin.Engine {
	router := gin.Default()
	router.Use(func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.Next() })
	router.POST("/internal/v1/judge/jobs/:jobId/artifacts", func(c *gin.Context) { cfg.Judge.ArtifactHTTP(c.Writer, c.Request, c.Param("jobId"), "") })
	router.GET("/internal/v1/judge/jobs/:jobId/artifacts/:artifactId", func(c *gin.Context) {
		cfg.Judge.ArtifactHTTP(c.Writer, c.Request, c.Param("jobId"), c.Param("artifactId"))
	})
	router.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	router.POST("/internal/v1/contests/:contestId/problems/:problemId/submissions", contestSubmissionHandler(cfg))
	router.PUT("/internal/v1/problems/:id/package", problemPackageHandler(cfg))
	router.POST("/internal/v1/problems/:id/test-submissions", testSubmissionHandler(cfg))
	for _, method := range []string{"GET", "HEAD"} {
		router.Handle(method, "/internal/v1/submissions/:submissionId/log", downloadHandler(cfg, false, "judge_log"))
		router.Handle(method, "/internal/v1/admin/submissions/:submissionId/source", downloadHandler(cfg, true, "source"))
		router.Handle(method, "/internal/v1/admin/submissions/:submissionId/jobs/:jobId/result", downloadHandler(cfg, true, "judge_result"))
		router.Handle(method, "/internal/v1/admin/submissions/:submissionId/jobs/:jobId/log", downloadHandler(cfg, true, "judge_log"))
	}
	// Browser-facing routes belong exclusively to the Web/Admin BFFs.
	router.Any("/api/v1/*path", retiredBrowserAPI)
	router.Any("/api/v1", retiredBrowserAPI)
	return router
}

func contestSubmissionHandler(cfg Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := internalBFFUser(c, cfg.WebBFFToken, cfg.Auth, false)
		if !ok {
			return
		}
		problemID, ok := numericID(c, "problemId")
		if !ok {
			return
		}
		contestID, ok := numericID(c, "contestId")
		if !ok {
			return
		}
		if err := cfg.Submissions.CanCreate(c, user.ID, problemID, contestID); err != nil {
			c.JSON(409, gin.H{"code": 4092, "error": "submission is not allowed"})
			return
		}
		staged, status, err := streamSingleFile(c, cfg.ArtifactRoot, cfg.Submissions.MaxSourceSize())
		if err != nil {
			if status == http.StatusConflict {
				c.JSON(status, gin.H{"code": 4092, "error": "submission is not allowed"})
				return
			}
			c.JSON(status, gin.H{"code": uploadErrorCode(status), "error": "invalid submission upload"})
			return
		}
		defer os.Remove(staged.Path)
		value, err := cfg.Submissions.CreateStaged(auth.WithSession(c.Request.Context(), c.GetHeader("X-SOJ-Session")), user.ID, problemID, contestID, staged)
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{"code": 4092, "error": "submission is not allowed"})
			return
		}
		c.Header("X-SOJ-SHA256", staged.SHA256)
		c.Header("X-SOJ-Size-Bytes", strconv.FormatInt(staged.Size, 10))
		c.JSON(http.StatusCreated, gin.H{"id": value.ID, "status": value.Status, "createdAt": value.CreatedAt})
	}
}

func problemPackageHandler(cfg Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := internalBFFUser(c, cfg.AdminBFFToken, cfg.Auth, true)
		if !ok {
			return
		}
		id, ok := numericID(c, "id")
		if !ok {
			return
		}
		if err := cfg.Problems.CanAdminSetPackage(c, id); err != nil {
			if errors.Is(err, problems.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"code": 4040, "error": "problem not found"})
				return
			}
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": 5030, "error": "service temporarily unavailable"})
			return
		}
		staged, status, err := streamSingleFile(c, cfg.ArtifactRoot, cfg.Problems.MaxJudgeProgramSize())
		if err != nil {
			c.JSON(status, gin.H{"code": uploadErrorCode(status), "error": "invalid problem package"})
			return
		}
		defer os.Remove(staged.Path)
		problem, err := cfg.Problems.AdminSetPackageStaged(auth.WithSession(c.Request.Context(), c.GetHeader("X-SOJ-Session")), user.ID, id, cfg.ArtifactRoot, staged.Path, staged.SHA256, staged.Size)
		if errors.Is(err, problems.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"code": 4040, "error": "problem not found"})
			return
		}
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 4001, "error": "invalid problem package"})
			return
		}
		c.Header("X-SOJ-SHA256", staged.SHA256)
		c.Header("X-SOJ-Size-Bytes", strconv.FormatInt(staged.Size, 10))
		c.JSON(http.StatusOK, gin.H{"id": problem.ID, "title": problem.Title, "difficulty": problem.Difficulty, "tags": problem.Tags, "content": problem.Content, "enabled": problem.Enabled, "partitions": problem.Partitions, "createdAt": problem.CreatedAt, "updatedAt": problem.UpdatedAt, "package": gin.H{"artifactId": problem.Package.ArtifactID, "sha256": problem.Package.SHA256, "sizeBytes": problem.Package.SizeBytes, "manifest": problem.Package.Manifest, "uploadedAt": problem.Package.UploadedAt}})
	}
}

func testSubmissionHandler(cfg Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := internalBFFUser(c, cfg.AdminBFFToken, cfg.Auth, true)
		if !ok {
			return
		}
		id, ok := numericID(c, "id")
		if !ok {
			return
		}
		if err := cfg.Submissions.CanCreateAdminTest(c, id); err != nil {
			c.JSON(http.StatusConflict, gin.H{"code": 4092, "error": "test submission is not allowed"})
			return
		}
		staged, status, err := streamSingleFile(c, cfg.ArtifactRoot, cfg.Submissions.MaxSourceSize())
		if err != nil {
			c.JSON(status, gin.H{"code": uploadErrorCode(status), "error": "invalid test submission"})
			return
		}
		defer os.Remove(staged.Path)
		value, err := cfg.Submissions.CreateAdminTestStaged(auth.WithSession(c.Request.Context(), c.GetHeader("X-SOJ-Session")), user.ID, id, staged)
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{"code": 4092, "error": "test submission is not allowed"})
			return
		}
		c.Header("X-SOJ-SHA256", staged.SHA256)
		c.Header("X-SOJ-Size-Bytes", strconv.FormatInt(staged.Size, 10))
		c.JSON(http.StatusCreated, gin.H{"id": value.ID, "status": value.Status, "createdAt": value.CreatedAt})
	}
}

func numericID(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 4001, "error": "invalid numeric id"})
		return 0, false
	}
	return id, true
}

func retiredBrowserAPI(c *gin.Context) {
	c.JSON(http.StatusGone, gin.H{"error": "use the Web or Admin BFF"})
}

func internalBFFUser(c *gin.Context, expectedToken string, service *auth.Service, admin bool) (auth.User, bool) {
	authorization, sessions := c.Request.Header.Values("Authorization"), c.Request.Header.Values("X-SOJ-Session")
	if len(authorization) != 1 || len(sessions) != 1 || !strings.HasPrefix(authorization[0], "Bearer ") || sessions[0] == "" || subtle.ConstantTimeCompare([]byte(expectedToken), []byte(strings.TrimPrefix(authorization[0], "Bearer "))) != 1 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 4010, "error": "invalid service credentials"})
		return auth.User{}, false
	}
	user, ok := service.CurrentUser(c.Request.Context(), sessions[0])
	if !ok {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 4010, "error": "invalid browser session"})
		return auth.User{}, false
	}
	if admin && user.Role != auth.RoleAdmin {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 4030, "error": "operation is not authorized"})
		return auth.User{}, false
	}
	return user, true
}
