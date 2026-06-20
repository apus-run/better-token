package grpc_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/storage/memory"

	btgrpc "github.com/apus-run/better-token/plugins/grpc"
)

// authHealthServer 在标准 Health 服务里断言 AuthContext 已注入。
type authHealthServer struct {
	healthpb.UnimplementedHealthServer
	t *testing.T
}

func (s *authHealthServer) Check(ctx context.Context, _ *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	loginID, err := core.RequireLoginID(ctx)
	if err != nil {
		s.t.Errorf("RequireLoginID failed: %v", err)
		return nil, err
	}
	if loginID != "1001" {
		s.t.Errorf("loginID = %q", loginID)
	}
	return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, nil
}

func dial(t *testing.T, manager *core.Manager, clientOpts ...btgrpc.ClientOption) (healthpb.HealthClient, func()) {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer(grpc.UnaryInterceptor(btgrpc.UnaryServerInterceptor(manager)))
	healthpb.RegisterHealthServer(srv, &authHealthServer{t: t})
	go func() { _ = srv.Serve(lis) }()

	dialOpts := []grpc.DialOption{
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	if len(clientOpts) > 0 {
		dialOpts = append(dialOpts, grpc.WithUnaryInterceptor(btgrpc.UnaryClientInterceptor(clientOpts...)))
	}
	conn, err := grpc.NewClient("passthrough:///bufnet", dialOpts...)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	return healthpb.NewHealthClient(conn), func() {
		_ = conn.Close()
		srv.Stop()
	}
}

func TestUnaryServerInterceptorAcceptsValidToken(t *testing.T) {
	manager := core.NewManager(memory.NewStore())
	if _, err := manager.Login(context.Background(), "1001", "access-1"); err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	client, cleanup := dial(t, manager, btgrpc.WithTokenSource(func(context.Context) (core.TokenValue, bool) {
		return "access-1", true
	}))
	defer cleanup()

	if _, err := client.Check(context.Background(), &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("Check with valid token failed: %v", err)
	}
}

func TestUnaryServerInterceptorRejectsMissingToken(t *testing.T) {
	manager := core.NewManager(memory.NewStore())
	client, cleanup := dial(t, manager)
	defer cleanup()

	_, err := client.Check(context.Background(), &healthpb.HealthCheckRequest{})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing token code = %v, want Unauthenticated", status.Code(err))
	}
}

func TestUnaryServerInterceptorRejectsInvalidToken(t *testing.T) {
	manager := core.NewManager(memory.NewStore())
	client, cleanup := dial(t, manager, btgrpc.WithTokenSource(func(context.Context) (core.TokenValue, bool) {
		return "bogus", true
	}))
	defer cleanup()

	_, err := client.Check(context.Background(), &healthpb.HealthCheckRequest{})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("invalid token code = %v, want Unauthenticated", status.Code(err))
	}
}
