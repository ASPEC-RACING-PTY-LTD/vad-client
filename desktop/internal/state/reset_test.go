package state

import (
	"path/filepath"
	"testing"
)

func TestResetSyncInScopesToConfigs(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, cfg := range []string{"a", "b"} {
		if err := s.PutFile(FileEntry{ConfigID: cfg, RelPath: "x.txt", Size: 1, MTime: 1, Status: "synced"}); err != nil {
			t.Fatal(err)
		}
		s.AddHistory(HistoryEntry{ConfigID: cfg, RelPath: "x.txt", Status: "uploaded"})
	}
	n, err := s.ResetSyncIn([]string{"a"})
	if err != nil || n != 1 {
		t.Fatalf("ResetSyncIn(a) = %d, %v", n, err)
	}
	if h, _ := s.HistoryIn("", []string{"a"}, 10); len(h) != 0 {
		t.Fatalf("history of a kept: %+v", h)
	}
	if h, _ := s.HistoryIn("", []string{"b"}, 10); len(h) != 1 {
		t.Fatalf("history of b = %d, want 1", len(h))
	}
	if n, err := s.ResetSyncIn(nil); err != nil || n != 1 {
		t.Fatalf("ResetSyncIn(nil) = %d, %v", n, err)
	}
}
