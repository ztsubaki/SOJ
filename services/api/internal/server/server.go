// Package server assembles and runs the Control Plane transports.
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	judgev1 "github.com/ztsubaki/soj/proto/soj/judge/v1"
	sojv1 "github.com/ztsubaki/soj/proto/soj/v1"
	"github.com/ztsubaki/soj/services/api/internal/db"
	"github.com/ztsubaki/soj/services/api/internal/modules/artifacts"
	"github.com/ztsubaki/soj/services/api/internal/modules/auth"
	"github.com/ztsubaki/soj/services/api/internal/modules/contests"
	"github.com/ztsubaki/soj/services/api/internal/modules/judge"
	"github.com/ztsubaki/soj/services/api/internal/modules/problems"
	"github.com/ztsubaki/soj/services/api/internal/modules/stats"
	"github.com/ztsubaki/soj/services/api/internal/modules/submissions"
	"github.com/ztsubaki/soj/services/api/internal/modules/system"
	"github.com/ztsubaki/soj/services/api/internal/rpcauth"
	"github.com/ztsubaki/soj/services/api/internal/serverconfig"
	"github.com/ztsubaki/soj/services/api/internal/transport/httpapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func Run(ctx context.Context) error {
	databaseURL, err := serverconfig.Required("DATABASE_URL")
	if err != nil {
		return err
	}
	grpcAddr, err := serverconfig.Required("GRPC_ADDR")
	if err != nil {
		return err
	}
	legacyInsecureGRPCDev, err := serverconfig.Bool("GRPC_INSECURE_DEV", os.Getenv("GRPC_INSECURE_DEV"), false)
	if err != nil {
		return err
	}
	if legacyInsecureGRPCDev && !serverconfig.IsLoopbackAddress(grpcAddr) {
		return errors.New("GRPC_INSECURE_DEV requires a loopback GRPC_ADDR")
	}
	agentToken, err := serverconfig.Required("AGENT_SERVICE_TOKEN")
	if err != nil {
		return err
	}
	webBFFToken, err := serverconfig.Required("WEB_BFF_SERVICE_TOKEN")
	if err != nil {
		return err
	}
	adminBFFToken, err := serverconfig.Required("ADMIN_BFF_SERVICE_TOKEN")
	if err != nil {
		return err
	}

	shutdownTimeout, err := serverconfig.Duration("SHUTDOWN_TIMEOUT", 20*time.Second)
	if err != nil {
		return err
	}

	databasePool, err := db.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer databasePool.Close()
	listener, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		return fmt.Errorf("listen gRPC: %w", err)
	}
	defer listener.Close()
	grpcOptions := []grpc.ServerOption{
		grpc.UnaryInterceptor(rpcauth.UnaryInterceptor(agentToken, webBFFToken, adminBFFToken)),
		grpc.MaxRecvMsgSize(64 << 20),
		grpc.MaxSendMsgSize(64 << 20),
	}
	if !legacyInsecureGRPCDev {
		tlsCertFile, err := serverconfig.Required("GRPC_TLS_CERT_FILE")
		if err != nil {
			return err
		}
		tlsKeyFile, err := serverconfig.Required("GRPC_TLS_KEY_FILE")
		if err != nil {
			return err
		}
		cert, err := tls.LoadX509KeyPair(tlsCertFile, tlsKeyFile)
		if err != nil {
			return fmt.Errorf("load gRPC TLS certificate: %w", err)
		}
		grpcOptions = append(grpcOptions, grpc.Creds(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}})))
	}
	grpcServer := grpc.NewServer(grpcOptions...)
	artifactRoot := serverconfig.String("ARTIFACT_ROOT", "data/artifacts")
	if err := ensureArtifactRootWritable(artifactRoot); err != nil {
		return err
	}
	judgeControl := judge.NewControlServer(databasePool, artifactRoot, agentToken)
	judgeControl.SetMaxArtifactSize(serverconfig.Megabytes("MAX_AGENT_ARTIFACT_SIZE_MB", judge.DefaultMaxAgentArtifactSize))
	judgev1.RegisterJudgeControlServiceServer(grpcServer, judgeControl)
	sessionTTL, err := serverconfig.Duration("SESSION_TTL", 72*time.Hour)
	if err != nil {
		return err
	}
	registrationEmailDomains, err := serverconfig.EmailDomains("REGISTRATION_EMAIL_DOMAINS")
	if err != nil {
		return err
	}
	emailSender, err := auth.NewRegistrationEmailSender(auth.SMTPConfig{
		Host:         serverconfig.String("SMTP_HOST", ""),
		Port:         serverconfig.String("SMTP_PORT", "587"),
		Username:     serverconfig.String("SMTP_USERNAME", ""),
		Password:     os.Getenv("SMTP_PASSWORD"),
		From:         serverconfig.String("SMTP_FROM", ""),
		TLSMode:      serverconfig.String("SMTP_TLS_MODE", "starttls"),
		PlatformName: serverconfig.String("PLATFORM_NAME", "SOJ"),
	})
	if err != nil {
		return err
	}
	authService, err := auth.NewPostgresServiceWithEmailSender(ctx, databasePool, artifactRoot, sessionTTL, registrationEmailDomains, emailSender)
	if err != nil {
		return err
	}
	sojv1.RegisterAuthServiceServer(grpcServer, auth.NewGRPCServer(authService))
	sojv1.RegisterAdminUserServiceServer(grpcServer, auth.NewAdminGRPCServer(authService))

	go artifacts.RunCleanup(ctx, databasePool, artifactRoot, time.Minute)
	go judge.RunAgentOfflineScanner(ctx, databasePool, time.Second)

	problemService := problems.NewService(databasePool, artifactRoot, serverconfig.Megabytes("MAX_JUDGE_PROGRAM_SIZE_MB", problems.DefaultMaxJudgeProgramSize))
	contestService := contests.NewService(databasePool, artifactRoot)
	submissionService := submissions.NewService(databasePool, artifactRoot, serverconfig.Megabytes("MAX_SOURCE_SIZE_MB", submissions.DefaultMaxSourceSize))
	statsService := stats.NewService(databasePool)
	sojv1.RegisterAdminProblemServiceServer(grpcServer, problems.NewAdminGRPCServer(problemService, authService))
	sojv1.RegisterAdminContestServiceServer(grpcServer, contests.NewAdminGRPCServer(contestService, authService))
	sojv1.RegisterContestServiceServer(grpcServer, contests.NewPublicGRPCServer(contestService, authService))
	sojv1.RegisterSubmissionServiceServer(grpcServer, submissions.NewGRPCServer(submissionService, authService))
	sojv1.RegisterAdminSubmissionServiceServer(grpcServer, submissions.NewAdminGRPCServer(submissionService, authService))
	sojv1.RegisterAdminSystemServiceServer(grpcServer, system.NewGRPCServer(databasePool, authService))
	sojv1.RegisterStatsServiceServer(grpcServer, stats.NewGRPCServer(statsService, authService))
	router := httpapi.NewRouter(httpapi.Config{
		Pool: databasePool, ArtifactRoot: artifactRoot, Judge: judgeControl,
		WebBFFToken: webBFFToken, AdminBFFToken: adminBFFToken, Auth: authService,
		Problems: problemService, Submissions: submissionService,
	})

	httpServer := &http.Server{
		Addr:              serverconfig.String("HTTP_ADDR", ":8080"),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	grpcErrors := make(chan error, 1)
	go func() { grpcErrors <- grpcServer.Serve(listener) }()
	httpErrors := make(chan error, 1)
	go func() { httpErrors <- httpServer.ListenAndServe() }()

	select {
	case err := <-grpcErrors:
		if err != nil {
			return fmt.Errorf("serve gRPC: %w", err)
		}
		return nil
	case err := <-httpErrors:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	grpcDone := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(grpcDone)
	}()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		grpcServer.Stop()
		return fmt.Errorf("shutdown HTTP: %w", err)
	}
	select {
	case <-grpcDone:
	case <-shutdownCtx.Done():
		grpcServer.Stop()
	}
	return nil
}

// ensureArtifactRootWritable fails startup before an upload reaches Gin when
// the local Artifact backend has been mounted with unsuitable permissions.
func ensureArtifactRootWritable(root string) error {
	if err := os.MkdirAll(root, 0700); err != nil {
		return fmt.Errorf("create ARTIFACT_ROOT %q: %w", root, err)
	}
	probe, err := os.CreateTemp(root, ".write-probe-*")
	if err != nil {
		return fmt.Errorf("ARTIFACT_ROOT %q is not writable: %w", root, err)
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("close ARTIFACT_ROOT write probe: %w", err)
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("remove ARTIFACT_ROOT write probe: %w", err)
	}
	return nil
}
