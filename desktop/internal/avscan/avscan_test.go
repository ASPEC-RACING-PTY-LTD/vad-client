package avscan

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckCleanFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "hello.txt")
	if err := os.WriteFile(p, []byte("hello from VaultDock"), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := Check(p, "hello.txt")
	if err != nil {
		t.Fatalf("clean file: %v (%s)", err, v)
	}
	t.Logf("verdict: %s", v)
}
