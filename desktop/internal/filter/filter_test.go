package filter

import (
	"testing"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/config"
)

func TestFilterRules(t *testing.T) {
	c := &config.SyncConfig{
		Name: "Medal", Kind: "folder", LocalPath: `C:\Medal`, Destination: "/Medal", Recursive: true,
		IncludeExtensions: []string{".MP4", "mkv"}, ExcludeSubfolders: []string{"Temp"}, MinSize: 1 << 20,
		ExcludePatterns: []string{"*_draft*", "Archive/**"}, ModifiedAfter: "2026-01-01", SkipHidden: true,
	}
	c.Normalize()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	f := New(c)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local)
	cases := []struct {
		rel    string
		size   int64
		mtime  time.Time
		hidden bool
		want   bool
	}{
		{"Valorant/clip.mp4", 5 << 20, now, false, true},
		{"clip.MKV", 5 << 20, now, false, true},
		{"Valorant/clip.mov", 5 << 20, now, false, false},
		{"Temp/clip.mp4", 5 << 20, now, false, false},
		{"temp/sub/clip.mp4", 5 << 20, now, false, false},
		{"Valorant/tiny.mp4", 100, now, false, false},
		{"Valorant/clip_draft1.mp4", 5 << 20, now, false, false},
		{"Archive/x/clip.mp4", 5 << 20, now, false, false},
		{"Valorant/old.mp4", 5 << 20, time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local), false, false},
		{"Valorant/hidden.mp4", 5 << 20, now, true, false},
		{"Valorant/recording.mp4.part", 5 << 20, now, false, false},
		{"desktop.ini", 10, now, false, false},
	}
	for _, tc := range cases {
		got, why := f.Match(tc.rel, tc.size, tc.mtime, tc.hidden)
		if got != tc.want {
			t.Errorf("%s: got %v (%s) want %v", tc.rel, got, why, tc.want)
		}
	}
	if f.WalkDir("Temp", false) || f.WalkDir("Archive", false) || !f.WalkDir("Valorant", false) {
		t.Error("directory pruning wrong")
	}
}

func TestNonRecursiveAndIncludeSubfolders(t *testing.T) {
	c := &config.SyncConfig{Name: "x", Kind: "folder", LocalPath: `C:\x`, Destination: "/", Recursive: false}
	c.Normalize()
	f := New(c)
	if ok, _ := f.Match("a/b.txt", 1, time.Now(), false); ok {
		t.Error("non-recursive should skip subfolders")
	}
	if ok, _ := f.Match("b.txt", 1, time.Now(), false); !ok {
		t.Error("root files should match")
	}
	c2 := &config.SyncConfig{Name: "x", Kind: "folder", LocalPath: `C:\x`, Destination: "/", Recursive: true, IncludeSubfolders: []string{"Games/Valorant"}}
	c2.Normalize()
	f2 := New(c2)
	if !f2.WalkDir("Games", false) || !f2.WalkDir("Games/Valorant", false) || f2.WalkDir("Games/CS2", false) || f2.WalkDir("Other", false) {
		t.Error("include subfolder pruning wrong")
	}
	if ok, _ := f2.Match("Games/Valorant/a.mp4", 1, time.Now(), false); !ok {
		t.Error("included subfolder file should match")
	}
	if ok, _ := f2.Match("top.mp4", 1, time.Now(), false); ok {
		t.Error("root file should not match when subfolders are restricted")
	}
}

func TestDriveRootRejected(t *testing.T) {
	for _, p := range []string{`C:\`, `D:\`, `C:`} {
		c := &config.SyncConfig{Name: "x", Kind: "folder", LocalPath: p, Destination: "/"}
		c.Normalize()
		if err := c.Validate(); err == nil {
			t.Errorf("%s accepted as sync root", p)
		}
	}
}

// Hidden folders are skipped unless added explicitly, whatever SkipHidden
// says; a hidden folder inside an added one needs adding too.
func TestHiddenFolders(t *testing.T) {
	c := &config.SyncConfig{Name: "Clips", Kind: "folder", LocalPath: `C:\Clips`, Destination: "/Clips", Recursive: true, SkipHidden: false}
	c.Normalize()
	f := New(c)
	if f.WalkDir("HIDDEN", true) {
		t.Fatal("hidden folder walked without being added")
	}
	if !f.WalkDir("Visible", false) {
		t.Fatal("normal folder skipped")
	}
	if ok, _ := f.Match("Visible/.secret.mp4", 1, time.Now(), true); ok {
		t.Fatal("hidden file selected")
	}

	c.IncludeHidden = []string{"HIDDEN", "Nest/.a/.b"}
	c.Normalize()
	f = New(c)
	if !f.WalkDir("HIDDEN", true) || !f.WalkDir("hidden", true) {
		t.Fatal("added hidden folder skipped")
	}
	if ok, why := f.Match("HIDDEN/clip.mp4", 1, time.Now(), false); !ok {
		t.Fatalf("file in an added hidden folder: %s", why)
	}
	if f.WalkDir("HIDDEN/.inner", true) {
		t.Fatal("hidden folder inside an added one walked without being added")
	}
	if !f.WalkDir("Nest/.a", true) || !f.WalkDir("Nest/.a/.b", true) {
		t.Fatal("path to an added hidden folder not walked")
	}
	if ok, _ := f.Match("Other/clip.mp4", 1, time.Now(), false); !ok {
		t.Fatal("IncludeHidden narrowed the selection")
	}
}
