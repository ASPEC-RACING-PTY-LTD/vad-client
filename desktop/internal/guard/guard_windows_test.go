//go:build windows

package guard

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// fixture builds base\{alice,bob} with ProfilesDir = base.
func fixture(t *testing.T) (g *Guard, base string, alice Profile) {
	t.Helper()
	base = t.TempDir()
	for _, d := range []string{`alice\real`, `bob`} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	g, err := New(Env{ProfilesDir: base})
	if err != nil {
		t.Fatal(err)
	}
	return g, base, Profile{SID: "S-1-5-21-1-2-3-1001", Root: filepath.Join(base, "alice")}
}

func mklinkJ(t *testing.T, link, target string) {
	t.Helper()
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Skipf("mklink /J unavailable: %v: %s", err, out)
	}
}

func TestReparseJunction(t *testing.T) {
	g, base, alice := fixture(t)
	aliceDir := filepath.Join(base, "alice")
	mklinkJ(t, filepath.Join(aliceDir, "tobob"), filepath.Join(base, "bob"))
	mklinkJ(t, filepath.Join(aliceDir, "toreal"), filepath.Join(aliceDir, "real"))

	tests := []struct {
		name string
		op   Op
		rel  string
		want error
	}{
		{"plain write", OpWrite, `alice\real\f.txt`, nil},
		{"plain delete", OpDelete, `alice\real`, nil},
		{"write through junction", OpWrite, `alice\toreal\f.txt`, ErrReparse},
		{"restore through junction", OpRestoreWrite, `alice\toreal\new\f.txt`, ErrReparse},
		{"delete junction itself", OpDelete, `alice\toreal`, ErrReparse},
		{"read through junction in profile", OpRead, `alice\toreal\f.txt`, nil},
		{"read through junction to other", OpRead, `alice\tobob\f.txt`, ErrOutsideProfile},
		{"write through junction to other", OpWrite, `alice\tobob\f.txt`, ErrOutsideProfile},
		{"other direct", OpWrite, `bob\f.txt`, ErrOutsideProfile},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := g.Check(tt.op, filepath.Join(base, tt.rel), alice)
			if tt.want == nil && err != nil || tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("Check(%s, %s) = %v, want %v", tt.op, tt.rel, err, tt.want)
			}
		})
	}
}

func TestReparseJunctionToWindows(t *testing.T) {
	_, base, alice := fixture(t)
	g, err := New(SystemEnv())
	if err != nil {
		t.Fatal(err)
	}
	alice.Root = base // temp dir may live under the real profiles dir
	link := filepath.Join(base, "win")
	mklinkJ(t, link, os.Getenv("WINDIR"))
	if err := g.Check(OpRead, filepath.Join(link, "win.ini"), alice); err != nil {
		t.Fatalf("read via junction: %v", err)
	}
	if err := g.Check(OpWrite, filepath.Join(link, "x.txt"), alice); !errors.Is(err, ErrProtected) {
		t.Fatalf("write via junction to Windows = %v, want ErrProtected", err)
	}
}

func TestReparseSymlink(t *testing.T) {
	g, base, alice := fixture(t)
	target := filepath.Join(base, `alice\real\t.txt`)
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, `alice\link.txt`)
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := g.Check(OpWrite, link, alice); !errors.Is(err, ErrReparse) {
		t.Fatalf("write to symlink = %v, want ErrReparse", err)
	}
	if err := g.Check(OpRead, link, alice); err != nil {
		t.Fatalf("read of in-profile symlink = %v", err)
	}
}

func TestShortNames(t *testing.T) {
	g, base, alice := fixture(t)
	long := filepath.Join(base, "bobwithaverylongprofilename")
	if err := os.Mkdir(long, 0o755); err != nil {
		t.Fatal(err)
	}
	p, _ := windows.UTF16PtrFromString(long)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetShortPathName(p, &buf[0], uint32(len(buf)))
	short := windows.UTF16ToString(buf[:n])
	if err != nil || strings.EqualFold(short, long) {
		t.Skip("8.3 names disabled on this volume")
	}
	if err := g.Check(OpWrite, filepath.Join(short, "f.txt"), alice); !errors.Is(err, ErrOutsideProfile) {
		t.Fatalf("write via short name %s = %v, want ErrOutsideProfile", short, err)
	}
	// The temp directory itself may be given in 8.3 form (CI runners).
	n, err = windows.GetLongPathName(p, &buf[0], uint32(len(buf)))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(windows.UTF16ToString(buf[:n]), "new.txt")
	got, err := Canonicalize(filepath.Join(short, "new.txt"))
	if err != nil || !strings.EqualFold(got, want) {
		t.Fatalf("Canonicalize(short) = %q, %v; want %q", got, err, want)
	}
}

func TestSystemEnv(t *testing.T) {
	e := SystemEnv()
	if e.WinDir == "" || e.ProgramFiles == "" || e.ProgramData == "" || e.ProfilesDir == "" {
		t.Fatalf("SystemEnv incomplete: %+v", e)
	}
	if err := Check(OpWrite, filepath.Join(e.WinDir, "System32", "x.dll"), Profile{SID: "S-1"}); !errors.Is(err, ErrProtected) {
		t.Fatalf("Check(WinDir) = %v", err)
	}
}
