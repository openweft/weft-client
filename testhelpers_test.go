package weftclient

import (
	"context"
	"errors"
	"net"
	"testing"

	weftv1 "github.com/openweft/weft-proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// testServer is the in-process gRPC server we wire to a bufconn so
// tests don't need real sockets. Methods are stubbed with the
// behaviour each test wants via the function fields.
type testServer struct {
	weftv1.UnimplementedWeftAgentServer
	listProjectsFn func(context.Context, *weftv1.ListProjectsRequest) (*weftv1.ListProjectsResponse, error)
	watchEventsFn  func(*weftv1.WatchEventsRequest, grpc.ServerStreamingServer[weftv1.PlatformEvent]) error
}

func (s *testServer) ListProjects(ctx context.Context, in *weftv1.ListProjectsRequest) (*weftv1.ListProjectsResponse, error) {
	if s.listProjectsFn != nil {
		return s.listProjectsFn(ctx, in)
	}
	return &weftv1.ListProjectsResponse{}, nil
}

func (s *testServer) WatchEvents(in *weftv1.WatchEventsRequest, srv grpc.ServerStreamingServer[weftv1.PlatformEvent]) error {
	if s.watchEventsFn != nil {
		return s.watchEventsFn(in, srv)
	}
	return errors.New("not implemented")
}

// startBufServer spins up a gRPC server bound to a bufconn and
// returns a connected gRPC ClientConn (and a cleanup).
func startBufServer(t *testing.T, svr *testServer, dialOpts ...grpc.DialOption) (*grpc.ClientConn, *grpc.Server) {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	gs := grpc.NewServer()
	weftv1.RegisterWeftAgentServer(gs, svr)
	go func() { _ = gs.Serve(lis) }()
	opts := append([]grpc.DialOption{
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}, dialOpts...)
	conn, err := grpc.NewClient("passthrough:///bufnet", opts...)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() {
		conn.Close()
		gs.Stop()
	})
	return conn, gs
}
