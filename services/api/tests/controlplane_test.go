package tests

import (
	"context"
	"testing"
	"time"

	"github.com/ztsubaki/soj/services/api/internal/rpcauth"
	"github.com/ztsubaki/soj/services/api/internal/serverconfig"
	"github.com/ztsubaki/soj/services/api/internal/validation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestBusinessID(t *testing.T) {
	for _, value := range []string{"a", "A-1._~", "job-123", "0start"} {
		if !validation.BusinessID(value) {
			t.Errorf("BusinessID(%q) = false, want true", value)
		}
	}
	for _, value := range []string{"", ".leading", "_leading", "-leading", "~leading", "space here"} {
		if validation.BusinessID(value) {
			t.Errorf("BusinessID(%q) = true, want false", value)
		}
	}
}

func TestUnaryInterceptorRequiresDeadline(t *testing.T) {
	interceptor := rpcauth.UnaryInterceptor("agent", "web", "admin")
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer web"))
	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/soj.v1.AuthService/Login"}, func(context.Context, any) (any, error) {
		return "unexpected", nil
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %s, want %s", status.Code(err), codes.InvalidArgument)
	}
}

func TestUnaryInterceptorAcceptsDeadline(t *testing.T) {
	interceptor := rpcauth.UnaryInterceptor("agent", "web", "admin")
	base, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ctx := metadata.NewIncomingContext(base, metadata.Pairs("authorization", "Bearer web"))
	called := false
	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/soj.v1.AuthService/Login"}, func(context.Context, any) (any, error) {
		called = true
		return nil, nil
	})
	if err != nil || !called {
		t.Fatalf("interceptor = (%v, called=%v), want successful handler", err, called)
	}
}

func TestDevelopmentGRPCConfig(t *testing.T) {
	for _, address := range []string{"localhost:50051", "127.0.0.1:50051", "[::1]:50051"} {
		if !serverconfig.IsLoopbackAddress(address) {
			t.Errorf("IsLoopbackAddress(%q) = false, want true", address)
		}
	}
	for _, address := range []string{":50051", "0.0.0.0:50051", "192.168.1.10:50051", "not-an-address"} {
		if serverconfig.IsLoopbackAddress(address) {
			t.Errorf("IsLoopbackAddress(%q) = true, want false", address)
		}
	}
	if got, err := serverconfig.Bool("GRPC_INSECURE_DEV", "true", false); err != nil || !got {
		t.Fatalf("Bool(true) = (%v, %v), want (true, nil)", got, err)
	}
	if _, err := serverconfig.Bool("GRPC_INSECURE_DEV", "invalid", false); err == nil {
		t.Fatal("Bool(invalid) succeeded, want error")
	}
}
