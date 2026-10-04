package store

import (
	"context"
	"errors"
	"testing"
)

func TestEventLog(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	if evs, cursor, more, err := s.Events(ctx, 0, 10); err != nil || len(evs) != 0 || cursor != 0 || more {
		t.Fatalf("an empty log = %v %d %v %v", evs, cursor, more, err)
	}
	for i, k := range []string{"workspace.patterns", "embedding.switched", "workspace.schema"} {
		seq, err := s.AppendEvent(ctx, Event{Kind: k, Workspace: "kb", Client: "tui-1", Detail: "d"})
		if err != nil || seq != int64(i+1) {
			t.Fatalf("append %d = %d, %v", i, seq, err)
		}
	}
	evs, cursor, more, err := s.Events(ctx, 0, 2)
	if err != nil || len(evs) != 2 || cursor != 2 || !more || evs[0].Kind != "workspace.patterns" || evs[0].Client != "tui-1" || evs[0].At == 0 {
		t.Fatalf("first page = %+v %d %v %v", evs, cursor, more, err)
	}
	evs, cursor, more, err = s.Events(ctx, cursor, 2)
	if err != nil || len(evs) != 1 || cursor != 3 || more || evs[0].Kind != "workspace.schema" {
		t.Fatalf("second page = %+v %d %v %v", evs, cursor, more, err)
	}
	if evs, cursor, _, err := s.Events(ctx, -1, 10); err != nil || len(evs) != 0 || cursor != 3 {
		t.Fatalf("the head = %v %d %v", evs, cursor, err)
	}
	if _, _, _, err := s.Events(ctx, 4, 10); !errors.Is(err, ErrEventsExpired) {
		t.Fatalf("a cursor past the head: %v", err)
	}
}

// The log keeps EventsKept rows; a cursor older than them expires, the one just before the oldest
// does not, and seq keeps rising.
func TestEventLogPrunes(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	for range EventsKept + 5 {
		if _, err := s.AppendEvent(ctx, Event{Kind: "k"}); err != nil {
			t.Fatal(err)
		}
	}
	evs, cursor, _, err := s.Events(ctx, 5, EventsKept+10)
	if err != nil || len(evs) != EventsKept || evs[0].Seq != 6 || cursor != EventsKept+5 {
		t.Fatalf("from the oldest-1: %d events from %v, cursor %d, %v", len(evs), evs[0].Seq, cursor, err)
	}
	if _, _, _, err := s.Events(ctx, 4, 10); !errors.Is(err, ErrEventsExpired) {
		t.Fatalf("a pruned cursor: %v", err)
	}
	if seq, _ := s.AppendEvent(ctx, Event{Kind: "k"}); seq != EventsKept+6 {
		t.Fatalf("seq after pruning = %d", seq)
	}
}

func TestWorkspaceProviderOverride(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	w, err := s.AddWorkspace(ctx, "kb", "/kb", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if name, err := s.WorkspaceProvider(ctx, w.ID); err != nil || name != "" {
		t.Fatalf("a new workspace's override = %q, %v", name, err)
	}
	if err := s.SetWorkspaceProvider(ctx, w.ID, "nope"); !errors.Is(err, ErrNoProvider) {
		t.Fatalf("an unknown provider: %v", err)
	}
	if _, err := s.AddProvider(ctx, ProviderSpec{Name: "local", Kind: KindOllama, BaseURL: "http://127.0.0.1:11434", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWorkspaceProvider(ctx, w.ID, "local"); err != nil {
		t.Fatal(err)
	}
	if name, _ := s.WorkspaceProvider(ctx, w.ID); name != "local" {
		t.Fatalf("override = %q", name)
	}
	// a rename keeps the override (it is by id); a delete returns the workspace to the daemon's
	if err := s.UpdateProvider(ctx, "local", ProviderSpec{Name: "near", Kind: KindOllama, BaseURL: "http://127.0.0.1:11434", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if name, _ := s.WorkspaceProvider(ctx, w.ID); name != "near" {
		t.Fatalf("after the rename = %q", name)
	}
	if err := s.RemoveProvider(ctx, "near"); err != nil {
		t.Fatal(err)
	}
	if name, err := s.WorkspaceProvider(ctx, w.ID); err != nil || name != "" {
		t.Fatalf("after the delete = %q, %v", name, err)
	}
	ws, _ := s.Workspaces(ctx)
	if ws[0].ProviderID != nil {
		t.Fatalf("provider_id after the delete = %d, want NULL (ON DELETE SET NULL)", *ws[0].ProviderID)
	}
}

// The workspace settings of 000007–000010 refuse a workspace the store does not have; a full facet
// and diagnostic row reads back as written; an explicit blank include is kept.
func TestWorkspaceSettingsRefuseAnUnknownWorkspace(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	const nope = int64(999)
	if _, err := s.SchemaPath(ctx, nope); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("SchemaPath: %v", err)
	}
	if err := s.SetWorkspaceSchema(ctx, nope, "s.yaml"); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("SetWorkspaceSchema: %v", err)
	}
	if _, err := s.TextExtensions(ctx, nope); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("TextExtensions: %v", err)
	}
	if err := s.SetWorkspaceTextExtensions(ctx, nope, []string{".log"}); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("SetWorkspaceTextExtensions: %v", err)
	}
	if _, err := s.WorkspaceProvider(ctx, nope); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("WorkspaceProvider: %v", err)
	}
	if err := s.SetWorkspaceProvider(ctx, nope, ""); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("SetWorkspaceProvider: %v", err)
	}
	if err := s.SetWorkspacePatterns(ctx, nope, []string{"*.md"}, nil); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("SetWorkspacePatterns: %v", err)
	}
	w, err := s.AddWorkspace(ctx, "blank", "/blank", []string{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ws, _ := s.Workspaces(ctx)
	if len(ws) != 1 || ws[0].Include == nil || len(ws[0].Include) != 0 {
		t.Fatalf("an explicit blank include was not kept: %+v", ws)
	}
	sc := s.Workspace(w.ID)
	err = s.Write(ctx, func(tx *Tx) error {
		doc, err := sc.Documents(tx).Set(DocPath, "n.md").Set(DocVersion, "v").Set(DocActiveGen, int64(1)).
			Set(DocIndexer, "i").Set(DocIndexedAt, int64(0)).Insert()
		if err != nil {
			return err
		}
		if err := sc.FacetBatch(tx).Add(map[FacetField]any{FacetDoc: doc, FacetName: "status", FacetValue: "active"}).Flush(); err != nil {
			return err
		}
		return sc.DiagnosticBatch(tx).Add(map[DiagnosticField]any{DiagDoc: doc, DiagOrd: int64(0), DiagField: "type",
			DiagLine: int64(2), DiagRule: "enum", DiagMessage: "m"}).Flush()
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Read(ctx, func(tx *Tx) error {
		fs, err := sc.Facets(tx).Select()
		if err != nil || len(fs) != 1 || fs[0].WorkspaceID != w.ID || fs[0].Field != "status" || fs[0].Value != "active" || fs[0].DocID == 0 {
			t.Errorf("facet row = %+v, %v", fs, err)
		}
		ds, err := sc.Diagnostics(tx).Select()
		if err != nil || len(ds) != 1 || ds[0].WorkspaceID != w.ID || ds[0].Line != 2 || ds[0].Rule != "enum" || ds[0].Message != "m" || ds[0].Ord != 0 {
			t.Errorf("diagnostic row = %+v, %v", ds, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
