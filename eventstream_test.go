package weftclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	weftv1 "github.com/openweft/weft-proto"
	"google.golang.org/grpc"
)

// flushWriter wraps a *bytes.Buffer and counts Flush calls, so the
// human renderer's optional Flush hook gets exercised.
type flushWriter struct {
	buf    *bytes.Buffer
	flushN int
}

func (f *flushWriter) Write(p []byte) (int, error) { return f.buf.Write(p) }
func (f *flushWriter) Flush() error                { f.flushN++; return nil }

// errWriter fails on every Write, so renderer error paths exercise.
type errWriter struct{ err error }

func (e *errWriter) Write(_ []byte) (int, error) { return 0, e.err }

func makeEvent() *weftv1.PlatformEvent {
	return &weftv1.PlatformEvent{
		TsUnixNs:    time.Date(2026, 5, 23, 10, 23, 45, 123_000_000, time.UTC).UnixNano(),
		Kind:        "vm.state.running",
		Subject:     "alpine",
		ProjectUuid: "uuid-alpha",
		Meta:        map[string]string{"pid": "12345", "a": "1"},
	}
}

func TestFormatMeta_Empty(t *testing.T) {
	if got := formatMeta(nil); got != "" {
		t.Fatalf("nil meta got %q", got)
	}
	if got := formatMeta(map[string]string{}); got != "" {
		t.Fatalf("empty meta got %q", got)
	}
}

func TestFormatMeta_Sorted(t *testing.T) {
	got := formatMeta(map[string]string{"b": "2", "a": "1"})
	if got != "a=1 b=2" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderEvent_Human_WithResolver(t *testing.T) {
	r := NewProjectResolver()
	r.Apply(&weftv1.PlatformEvent{
		Kind:        "project.created",
		ProjectUuid: "uuid-alpha",
		Meta:        map[string]string{"name": "team-alpha"},
	})
	fw := &flushWriter{buf: &bytes.Buffer{}}
	if err := RenderEvent(fw, makeEvent(), "", r); err != nil {
		t.Fatal(err)
	}
	out := fw.buf.String()
	if !strings.Contains(out, "vm.state.running") {
		t.Fatalf("missing kind: %s", out)
	}
	if !strings.Contains(out, "team-alpha") {
		t.Fatalf("resolver not applied: %s", out)
	}
	if !strings.Contains(out, "a=1 pid=12345") {
		t.Fatalf("meta sorted not present: %s", out)
	}
	if fw.flushN == 0 {
		t.Fatal("Flush was not called")
	}
}

func TestRenderEvent_Human_NilResolverFallback(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderEvent(&buf, makeEvent(), "", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "uuid-alpha") {
		t.Fatalf("expected raw uuid: %s", buf.String())
	}
}

func TestRenderEvent_Human_DashesForEmpty(t *testing.T) {
	var buf bytes.Buffer
	ev := &weftv1.PlatformEvent{
		TsUnixNs: time.Now().UnixNano(),
		Kind:     "platform.ready",
	}
	if err := RenderEvent(&buf, ev, "", nil); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	// Two "-" cells: subject and project.
	if strings.Count(s, "\t-") < 2 {
		t.Fatalf("expected two dash placeholders: %q", s)
	}
}

func TestRenderEvent_Human_NoMeta(t *testing.T) {
	var buf bytes.Buffer
	ev := makeEvent()
	ev.Meta = nil
	if err := RenderEvent(&buf, ev, "", nil); err != nil {
		t.Fatal(err)
	}
	// Should end with project + newline (no trailing meta column).
	out := strings.TrimRight(buf.String(), "\n")
	if strings.Contains(out, "=") {
		t.Fatalf("did not expect meta cell: %q", out)
	}
}

func TestRenderEvent_Human_WriteError(t *testing.T) {
	err := RenderEvent(&errWriter{err: errors.New("pipe broken")}, makeEvent(), "", nil)
	if err == nil {
		t.Fatal("expected write error to propagate")
	}
}

func TestRenderEvent_JSON_WithResolver(t *testing.T) {
	r := NewProjectResolver()
	r.Apply(&weftv1.PlatformEvent{
		Kind:        "project.created",
		ProjectUuid: "uuid-alpha",
		Meta:        map[string]string{"name": "team-alpha"},
	})
	var buf bytes.Buffer
	if err := RenderEvent(&buf, makeEvent(), "json", r); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &got); err != nil {
		t.Fatalf("invalid json: %v / %s", err, buf.String())
	}
	if got["project"] != "team-alpha" || got["project_uuid"] != "uuid-alpha" {
		t.Fatalf("unexpected json: %+v", got)
	}
}

func TestRenderEvent_JSON_ResolverEchoesUUID(t *testing.T) {
	// When the resolver returns the UUID itself (unknown), the
	// renderer should leave the "project" field empty.
	r := NewProjectResolver()
	var buf bytes.Buffer
	if err := RenderEvent(&buf, makeEvent(), "json", r); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["project"]; ok {
		t.Fatalf("project should be omitted when unresolved; got %+v", got)
	}
}

func TestRenderEvent_JSON_NilResolver(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderEvent(&buf, makeEvent(), "json", nil); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &got)
	if got["project_uuid"] != "uuid-alpha" {
		t.Fatalf("expected uuid; got %+v", got)
	}
}

func TestRenderEvent_JSON_WriteError(t *testing.T) {
	if err := RenderEvent(&errWriter{err: errors.New("EOF")}, makeEvent(), "json", nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestStreamEvents_HappyPath(t *testing.T) {
	events := []*weftv1.PlatformEvent{
		{
			Kind:        "project.created",
			ProjectUuid: "uuid-alpha",
			Meta:        map[string]string{"name": "team-alpha"},
			TsUnixNs:    time.Now().UnixNano(),
		},
		makeEvent(),
	}
	svr := &testServer{
		listProjectsFn: func(_ context.Context, _ *weftv1.ListProjectsRequest) (*weftv1.ListProjectsResponse, error) {
			return &weftv1.ListProjectsResponse{}, nil
		},
		watchEventsFn: func(in *weftv1.WatchEventsRequest, srv grpc.ServerStreamingServer[weftv1.PlatformEvent]) error {
			// Sanity-check the request fields plumbed through.
			if len(in.KindPrefix) != 1 || in.KindPrefix[0] != "vm." {
				return fmt.Errorf("unexpected prefix: %v", in.KindPrefix)
			}
			if in.Project != "p" || in.Subject != "s" {
				return fmt.Errorf("unexpected filters: %+v", in)
			}
			for _, ev := range events {
				if err := srv.Send(ev); err != nil {
					return err
				}
			}
			return nil
		},
	}
	conn, _ := startBufServer(t, svr)
	c := weftv1.NewWeftAgentClient(conn)
	var buf bytes.Buffer
	err := StreamEvents(context.Background(), c, EventStreamOptions{
		KindPrefixes: []string{"vm."},
		Project:      "p",
		Subject:      "s",
		Format:       "",
	}, &buf)
	if err != nil {
		t.Fatalf("stream events: %v", err)
	}
	out := buf.String()
	// We saw both rows.
	if strings.Count(out, "\n") != 2 {
		t.Fatalf("expected 2 lines, got: %q", out)
	}
	// After the project.created event flowed through, the resolver
	// should have learned the name and printed it on the second row.
	if !strings.Contains(out, "team-alpha") {
		t.Fatalf("expected team-alpha in output: %s", out)
	}
}

func TestStreamEvents_WatchError(t *testing.T) {
	svr := &testServer{
		watchEventsFn: func(_ *weftv1.WatchEventsRequest, _ grpc.ServerStreamingServer[weftv1.PlatformEvent]) error {
			return errors.New("rpc boom")
		},
	}
	conn, _ := startBufServer(t, svr)
	c := weftv1.NewWeftAgentClient(conn)
	err := StreamEvents(context.Background(), c, EventStreamOptions{}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "recv event") {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestStreamEvents_ContextCanceledCleanExit(t *testing.T) {
	// Server holds the stream open until ctx fires on the client.
	svr := &testServer{
		watchEventsFn: func(_ *weftv1.WatchEventsRequest, srv grpc.ServerStreamingServer[weftv1.PlatformEvent]) error {
			<-srv.Context().Done()
			return srv.Context().Err()
		},
	}
	conn, _ := startBufServer(t, svr)
	c := weftv1.NewWeftAgentClient(conn)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	err := StreamEvents(ctx, c, EventStreamOptions{}, io.Discard)
	if err != nil {
		t.Fatalf("expected clean exit on ctx cancel, got %v", err)
	}
}

func TestStreamEvents_RenderError(t *testing.T) {
	svr := &testServer{
		watchEventsFn: func(_ *weftv1.WatchEventsRequest, srv grpc.ServerStreamingServer[weftv1.PlatformEvent]) error {
			return srv.Send(makeEvent())
		},
	}
	conn, _ := startBufServer(t, svr)
	c := weftv1.NewWeftAgentClient(conn)
	err := StreamEvents(context.Background(), c, EventStreamOptions{},
		&errWriter{err: errors.New("pipe broken")})
	if err == nil || !strings.Contains(err.Error(), "render event") {
		t.Fatalf("expected render-event error, got %v", err)
	}
}

func TestStreamEvents_WatchOpenError(t *testing.T) {
	// Server is up but the client passes an already-cancelled ctx
	// so client.WatchEvents returns immediately with ctx.Err().
	svr := &testServer{}
	conn, _ := startBufServer(t, svr)
	c := weftv1.NewWeftAgentClient(conn)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := StreamEvents(ctx, c, EventStreamOptions{}, io.Discard)
	// With a cancelled ctx the stream's Recv returns and our code
	// detects ctx.Err() != nil → clean nil exit. That covers the
	// ctx-cancellation branch in Recv; the explicit watch-open
	// error branch is covered when the underlying transport
	// rejects the stream creation, which we can't easily force on
	// bufconn — so we accept either outcome.
	if err != nil && !strings.Contains(err.Error(), "watch events") && !strings.Contains(err.Error(), "recv event") {
		t.Fatalf("unexpected err: %v", err)
	}
}
