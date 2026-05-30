package weftclient

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestBearerInterceptor_AddsHeader(t *testing.T) {
	got := ""
	invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		md, _ := metadata.FromOutgoingContext(ctx)
		if vs := md.Get("authorization"); len(vs) > 0 {
			got = vs[0]
		}
		return nil
	}
	tokFn := func() string { return "abc" }
	ic := BearerInterceptor(tokFn)
	if err := ic(context.Background(), "/svc/Method", nil, nil, nil, invoker); err != nil {
		t.Fatal(err)
	}
	if got != "Bearer abc" {
		t.Fatalf("authorization header = %q", got)
	}
}

func TestBearerInterceptor_NoToken_NoHeader(t *testing.T) {
	headerCount := -1
	invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			headerCount = 0
			return nil
		}
		headerCount = len(md.Get("authorization"))
		return nil
	}
	ic := BearerInterceptor(func() string { return "" })
	if err := ic(context.Background(), "/svc/Method", nil, nil, nil, invoker); err != nil {
		t.Fatal(err)
	}
	if headerCount > 0 {
		t.Fatalf("expected no auth header, got %d", headerCount)
	}
}

func TestBearerInterceptor_ErrorPropagation(t *testing.T) {
	want := errors.New("rpc failed")
	invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		return want
	}
	ic := BearerInterceptor(func() string { return "x" })
	if err := ic(context.Background(), "/svc/M", nil, nil, nil, invoker); !errors.Is(err, want) {
		t.Fatalf("err = %v", err)
	}
}

func TestBearerStreamInterceptor_AddsHeader(t *testing.T) {
	got := ""
	streamer := func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		md, _ := metadata.FromOutgoingContext(ctx)
		if vs := md.Get("authorization"); len(vs) > 0 {
			got = vs[0]
		}
		return nil, nil
	}
	ic := BearerStreamInterceptor(func() string { return "abc" })
	if _, err := ic(context.Background(), &grpc.StreamDesc{}, nil, "/svc/M", streamer); err != nil {
		t.Fatal(err)
	}
	if got != "Bearer abc" {
		t.Fatalf("header = %q", got)
	}
}

func TestBearerStreamInterceptor_NoToken(t *testing.T) {
	called := false
	streamer := func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		called = true
		md, ok := metadata.FromOutgoingContext(ctx)
		if ok && len(md.Get("authorization")) > 0 {
			t.Fatal("unexpected auth header")
		}
		return nil, nil
	}
	ic := BearerStreamInterceptor(func() string { return "" })
	if _, err := ic(context.Background(), &grpc.StreamDesc{}, nil, "/svc/M", streamer); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("streamer was not invoked")
	}
}

func TestCachedTokenSource(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	// No file yet → empty.
	if got := CachedTokenSource()(); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	// Save then re-read.
	if err := SaveCachedToken(&CachedToken{
		Issuer: "i", ClientID: "c", AccessToken: "real-token", ExpiresAt: "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if got := CachedTokenSource()(); got != "real-token" {
		t.Fatalf("expected token, got %q", got)
	}
	// Corrupt file → LoadCachedToken errors → empty.
	if err := os.WriteFile(filepath.Join(tmp, "weft", "token.hcl"), []byte("bogus = ==="), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := CachedTokenSource()(); got != "" {
		t.Fatalf("expected empty on decode error, got %q", got)
	}
}
