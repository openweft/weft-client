package weftclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	weftv1 "github.com/openweft/weft-proto"
	"golang.org/x/crypto/ssh"
	"google.golang.org/grpc"
)

func TestDefaultSocket(t *testing.T) {
	// Happy path: $HOME points somewhere → ~/.weft/weft.sock.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	got := DefaultSocket()
	want := filepath.Join(tmp, ".weft", "weft.sock")
	if got != want {
		t.Fatalf("DefaultSocket() = %q, want %q", got, want)
	}
}

func TestDefaultSocket_NoHome(t *testing.T) {
	// Force os.UserHomeDir to return "" by clearing HOME on Unix.
	t.Setenv("HOME", "")
	// On macOS/Linux os.UserHomeDir falls back to passwd lookup. To
	// reliably get the "" path on darwin we also unset USER which
	// many fallback paths consult.
	t.Setenv("USER", "")
	got := DefaultSocket()
	// Either fallback (/tmp/weft.sock) or some resolved home — accept
	// the fallback when home really is empty; otherwise assert path
	// shape so we don't pin to one machine.
	if got != "/tmp/weft.sock" && !strings.HasSuffix(got, "/.weft/weft.sock") {
		t.Fatalf("unexpected default socket: %q", got)
	}
}

func TestStateString(t *testing.T) {
	cases := []struct {
		s    weftv1.VMState
		want string
	}{
		{weftv1.VMState_VM_STATE_RUNNING, "running"},
		{weftv1.VMState_VM_STATE_STOPPED, "stopped"},
		{weftv1.VMState_VM_STATE_NOT_CREATED, "not-created"},
		{weftv1.VMState_VM_STATE_ERROR, "error"},
		{weftv1.VMState_VM_STATE_UNSPECIFIED, "unknown"},
		{weftv1.VMState(99), "unknown"},
	}
	for _, tc := range cases {
		if got := StateString(tc.s); got != tc.want {
			t.Errorf("StateString(%v) = %q, want %q", tc.s, got, tc.want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{1024 * 1024 * 1024, "1.0 GiB"},
		{int64(1024) * 1024 * 1024 * 1024, "1.0 TiB"},
		{int64(1024) * 1024 * 1024 * 1024 * 1024, "1.0 PiB"},
		{int64(1024) * 1024 * 1024 * 1024 * 1024 * 1024, "1.0 EiB"},
		{1500, "1.5 KiB"},
	}
	for _, tc := range cases {
		if got := HumanBytes(tc.in); got != tc.want {
			t.Errorf("HumanBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestWithTimeoutOption(t *testing.T) {
	var o Options
	WithTimeout(7 * time.Second)(&o)
	if o.timeout != 7*time.Second {
		t.Fatalf("timeout = %v, want 7s", o.timeout)
	}
}

func TestWithSSHOption(t *testing.T) {
	var o Options
	WithSSH("/sock", "/key")(&o)
	if o.sshSocket != "/sock" || o.sshKey != "/key" {
		t.Fatalf("WithSSH set %+v", o)
	}
}

func TestDial_UnixSocketTimeout(t *testing.T) {
	// No listener on this socket path → grpc.WithBlock + a tiny
	// timeout makes Dial fail fast. Exercises the unix-socket
	// branch (no SSH key set) and the timeout cancellation.
	tmp := t.TempDir()
	sock := filepath.Join(tmp, "nope.sock")
	_, err := Dial(sock, WithTimeout(50*time.Millisecond))
	if err == nil {
		t.Fatal("expected dial error on missing socket")
	}
}

func TestDial_EmptySocketUsesDefault(t *testing.T) {
	// Pointing $HOME at a tempdir means DefaultSocket() is also
	// missing — Dial should still attempt and fail fast.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	_, err := Dial("", WithTimeout(50*time.Millisecond))
	if err == nil {
		t.Fatal("expected dial error on missing default socket")
	}
}

func TestDial_SSHKeyMissing(t *testing.T) {
	// SSH branch with a missing key file → DialOption returns an
	// error before we ever hit the network.
	tmp := t.TempDir()
	_, err := Dial("", WithSSH(filepath.Join(tmp, "sshd.sock"), filepath.Join(tmp, "no-such-key")))
	if err == nil {
		t.Fatal("expected ssh dial-option error")
	}
	if !strings.Contains(err.Error(), "ssh dial option") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDial_SSHDefaultSocketPath(t *testing.T) {
	// Empty SSH socket → fallback to $HOME/.weft/weft-ssh.sock.
	// Still expected to fail because we pass a missing key path,
	// but this exercises the home-resolution branch.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	_, err := Dial("", WithSSH("", filepath.Join(tmp, "missing-key")))
	if err == nil {
		t.Fatal("expected ssh dial-option error")
	}
}

func TestClient_DialError(t *testing.T) {
	tmp := t.TempDir()
	sock := filepath.Join(tmp, "nope.sock")
	c, conn, err := Client(sock, WithTimeout(50*time.Millisecond))
	if err == nil {
		t.Fatal("expected error")
	}
	if c != nil || conn != nil {
		t.Fatalf("expected nil client/conn on error, got %v %v", c, conn)
	}
	if !strings.Contains(err.Error(), "connect to weft at") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// silence unused import linter on os in cross-platform setups.
var _ = os.Getenv

// writeEd25519Key generates an ed25519 private key, PEM-encodes it
// (OpenSSH-compatible via marshalling), and writes it to disk so
// sshtransport.DialOption's "read private key" branch succeeds.
func writeEd25519Key(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// Marshal to OpenSSH PEM via the ssh package.
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(p, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDial_SSHHappyPathReached(t *testing.T) {
	// With a *valid* SSH key file the DialOption succeeds, so we
	// reach the final grpc.DialContext call. There's no SSH server
	// behind the socket so the dial itself fails — but the return
	// statement (and hence the coverage line) is reached.
	keyPath := writeEd25519Key(t)
	tmp := t.TempDir()
	sshSock := filepath.Join(tmp, "weft-ssh.sock")
	_, err := Dial("", WithSSH(sshSock, keyPath), WithTimeout(100*time.Millisecond))
	if err == nil {
		t.Fatal("expected dial failure on missing SSH listener")
	}
}

// startUnixGRPCServer binds a real gRPC server to a Unix socket so
// Dial / Client success paths can be exercised end-to-end.
func startUnixGRPCServer(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	sock := filepath.Join(tmp, "weft.sock")
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	gs := grpc.NewServer()
	weftv1.RegisterWeftAgentServer(gs, &testServer{
		listProjectsFn: func(_ context.Context, _ *weftv1.ListProjectsRequest) (*weftv1.ListProjectsResponse, error) {
			return &weftv1.ListProjectsResponse{}, nil
		},
	})
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(func() { gs.Stop() })
	return sock
}

func TestClient_Success(t *testing.T) {
	sock := startUnixGRPCServer(t)
	c, conn, err := Client(sock, WithTimeout(2*time.Second))
	if err != nil {
		t.Fatalf("Client failed: %v", err)
	}
	defer conn.Close()
	if c == nil {
		t.Fatal("nil typed client")
	}
	// Roundtrip a call to prove the interceptor chain works.
	if _, err := c.ListProjects(context.Background(), &weftv1.ListProjectsRequest{}); err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
}
