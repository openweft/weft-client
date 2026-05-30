package weftclient

import (
	"context"
	"errors"
	"testing"

	weftv1 "github.com/openweft/weft-proto"
)

func TestNewProjectResolver(t *testing.T) {
	r := NewProjectResolver()
	if r == nil || r.byID == nil {
		t.Fatal("resolver / map should be non-nil")
	}
}

func TestProjectResolver_Name_NilAndZeroValue(t *testing.T) {
	var nilR *ProjectResolver
	if got := nilR.Name("u"); got != "u" {
		t.Fatalf("nil resolver returned %q", got)
	}
	r := NewProjectResolver()
	if got := r.Name(""); got != "" {
		t.Fatalf("empty uuid should return empty; got %q", got)
	}
	if got := r.Name("missing"); got != "missing" {
		t.Fatalf("unknown should echo uuid; got %q", got)
	}
}

func TestProjectResolver_Bootstrap_Success(t *testing.T) {
	svr := &testServer{
		listProjectsFn: func(_ context.Context, _ *weftv1.ListProjectsRequest) (*weftv1.ListProjectsResponse, error) {
			return &weftv1.ListProjectsResponse{Projects: []*weftv1.ProjectInfo{
				{Uuid: "u1", Name: "alpha"},
				{Uuid: "u2", Name: "beta"},
			}}, nil
		},
	}
	conn, _ := startBufServer(t, svr)
	c := weftv1.NewWeftAgentClient(conn)
	r := NewProjectResolver()
	r.Bootstrap(context.Background(), c)
	if got := r.Name("u1"); got != "alpha" {
		t.Fatalf("u1 = %q", got)
	}
	if got := r.Name("u2"); got != "beta" {
		t.Fatalf("u2 = %q", got)
	}
}

func TestProjectResolver_Bootstrap_Failure(t *testing.T) {
	svr := &testServer{
		listProjectsFn: func(_ context.Context, _ *weftv1.ListProjectsRequest) (*weftv1.ListProjectsResponse, error) {
			return nil, errors.New("boom")
		},
	}
	conn, _ := startBufServer(t, svr)
	c := weftv1.NewWeftAgentClient(conn)
	r := NewProjectResolver()
	r.Bootstrap(context.Background(), c) // must not panic
	if got := r.Name("u1"); got != "u1" {
		t.Fatalf("expected echo, got %q", got)
	}
}

func TestProjectResolver_Apply(t *testing.T) {
	r := NewProjectResolver()
	// nil + missing project_uuid → no-op.
	r.Apply(nil)
	r.Apply(&weftv1.PlatformEvent{Kind: "project.created"})

	// project.created with Meta["name"].
	r.Apply(&weftv1.PlatformEvent{
		Kind:        "project.created",
		ProjectUuid: "u1",
		Meta:        map[string]string{"name": "alpha"},
	})
	if got := r.Name("u1"); got != "alpha" {
		t.Fatalf("created: %q", got)
	}

	// project.renamed with Meta["new_name"] — new_name wins.
	r.Apply(&weftv1.PlatformEvent{
		Kind:        "project.renamed",
		ProjectUuid: "u1",
		Meta:        map[string]string{"new_name": "alpha2", "name": "stale"},
	})
	if got := r.Name("u1"); got != "alpha2" {
		t.Fatalf("renamed (new_name): %q", got)
	}

	// project.renamed falling back to Meta["name"] when new_name is empty.
	r.Apply(&weftv1.PlatformEvent{
		Kind:        "project.renamed",
		ProjectUuid: "u1",
		Meta:        map[string]string{"new_name": "", "name": "alpha3"},
	})
	if got := r.Name("u1"); got != "alpha3" {
		t.Fatalf("renamed (name fallback): %q", got)
	}

	// project.created/renamed without any name field → no-op.
	r.Apply(&weftv1.PlatformEvent{
		Kind:        "project.created",
		ProjectUuid: "u9",
		Meta:        map[string]string{},
	})
	if got := r.Name("u9"); got != "u9" {
		t.Fatalf("expected echo, got %q", got)
	}

	// nil meta → still safe.
	r.Apply(&weftv1.PlatformEvent{
		Kind:        "project.created",
		ProjectUuid: "u10",
	})
	if got := r.Name("u10"); got != "u10" {
		t.Fatalf("nil meta should leave entry absent: %q", got)
	}

	// project.deleted removes.
	r.Apply(&weftv1.PlatformEvent{Kind: "project.deleted", ProjectUuid: "u1"})
	if got := r.Name("u1"); got != "u1" {
		t.Fatalf("delete should clear; got %q", got)
	}

	// Unknown kind → no-op.
	r.Apply(&weftv1.PlatformEvent{Kind: "vm.state.running", ProjectUuid: "u2"})
	if got := r.Name("u2"); got != "u2" {
		t.Fatalf("unrelated kind: %q", got)
	}
}
