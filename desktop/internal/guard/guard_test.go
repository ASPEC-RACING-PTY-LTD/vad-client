package guard

import (
	"errors"
	"testing"
)

func testGuard(t *testing.T) *Guard {
	t.Helper()
	g, err := New(Env{
		WinDir:          `C:\Windows`,
		ProgramFiles:    `C:\Program Files`,
		ProgramFilesX86: `C:\Program Files (x86)`,
		ProgramFilesX64: `C:\Program Files`,
		ProgramData:     `C:\ProgramData`,
		ProfilesDir:     `C:\Users`,
		SystemDrive:     `C:`,
	})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestCheck(t *testing.T) {
	g := testGuard(t)
	alice := Profile{SID: "S-1-5-21-1-2-3-1001", Root: `C:\Users\alice`}
	aliceBob := alice
	aliceBob.AuthorisedRoots = []string{`C:\Users\bob`}
	noSID := Profile{Root: `C:\Users\alice`}

	tests := []struct {
		name string
		op   Op
		path string
		prof Profile
		want error
	}{
		// Own profile.
		{"own write", OpWrite, `C:\Users\alice\Documents\a.txt`, alice, nil},
		{"own read", OpRead, `C:\Users\alice\Documents\a.txt`, alice, nil},
		{"own delete", OpDelete, `C:\Users\alice\Documents\a.txt`, alice, nil},
		{"own restore", OpRestoreWrite, `C:\Users\alice\Documents\a.txt`, alice, nil},
		{"restore without SID", OpRestoreWrite, `C:\Users\alice\Documents\a.txt`, noSID, ErrOutsideProfile},
		{"case and slashes", OpWrite, `c:/USERS/Alice/docs//./a.txt`, alice, nil},
		{"verbatim prefix", OpWrite, `\\?\C:\Users\alice\a.txt`, alice, nil},
		{"own profile root write", OpWrite, `C:\Users\alice`, alice, nil},

		// Other profiles.
		{"other write", OpWrite, `C:\Users\bob\a.txt`, alice, ErrOutsideProfile},
		{"other read", OpRead, `C:\Users\bob\a.txt`, alice, ErrOutsideProfile},
		{"other delete", OpDelete, `C:\Users\bob`, alice, ErrOutsideProfile},
		{"other authorised", OpWrite, `C:\Users\bob\a.txt`, aliceBob, nil},
		{"prefix sibling", OpWrite, `C:\Users\alice2\a.txt`, alice, ErrOutsideProfile},
		{"dotdot to other", OpWrite, `C:\Users\alice\..\bob\a.txt`, alice, ErrOutsideProfile},
		{"trailing dot other", OpWrite, `C:\Users\bob.\a.txt`, alice, ErrOutsideProfile},
		{"no profile", OpRead, `C:\Users\alice\a.txt`, Profile{}, ErrOutsideProfile},
		{"public allowed", OpWrite, `C:\Users\Public\Documents\a.txt`, alice, nil},
		{"default profile", OpWrite, `C:\Users\Default\NTUSER.DAT`, alice, ErrProtected},
		{"default profile read", OpRead, `C:\Users\Default User\x`, alice, ErrProtected},
		{"all users", OpWrite, `C:\Users\All Users\x`, alice, ErrProtected},
		{"profiles dir write", OpWrite, `C:\Users`, alice, ErrProtected},
		{"profiles dir read", OpRead, `C:\Users`, alice, nil},

		// System directories.
		{"windows write", OpWrite, `C:\Windows\System32\drivers\etc\hosts`, alice, ErrProtected},
		{"windows delete", OpDelete, `C:\Windows`, alice, ErrProtected},
		{"windows restore", OpRestoreWrite, `C:\Windows\win.ini`, alice, ErrProtected},
		{"windows read", OpRead, `C:\Windows\win.ini`, alice, nil},
		{"windows case", OpWrite, `c:\WINDOWS\x`, alice, ErrProtected},
		{"windows trailing dot", OpWrite, `C:\Windows.\x`, alice, ErrProtected},
		{"windows trailing space", OpWrite, `C:\Windows \x`, alice, ErrProtected},
		{"windows verbatim", OpWrite, `\\?\C:\Windows\x`, alice, ErrProtected},
		{"windows via dotdot", OpWrite, `C:\Users\alice\..\..\Windows\x`, alice, ErrProtected},
		{"windows sibling ok", OpWrite, `C:\WindowsApps2\x`, alice, nil},
		{"program files", OpWrite, `C:\Program Files\App\app.exe`, alice, ErrProtected},
		{"program files x86", OpDelete, `C:\Program Files (x86)\App`, alice, ErrProtected},
		{"program files read", OpRead, `C:\Program Files\App\settings.ini`, alice, nil},
		{"programdata write", OpWrite, `C:\ProgramData\Vendor\x`, alice, ErrProtected},
		{"programdata read", OpRead, `C:\ProgramData\Vendor\x`, alice, nil},
		{"programdata vaultdock read", OpRead, `C:\ProgramData\VaultDock\state.db`, alice, ErrProtected},
		{"programdata microsoft read", OpRead, `C:\ProgramData\Microsoft\Crypto\x`, alice, ErrProtected},

		// Volume roots and special entries.
		{"system root write", OpWrite, `C:\`, alice, ErrProtected},
		{"other root delete", OpDelete, `D:\`, alice, ErrProtected},
		{"bare drive write", OpWrite, `D:`, alice, ErrProtected},
		{"root read", OpRead, `D:\`, alice, nil},
		{"data dir write", OpWrite, `D:\Data\a.txt`, alice, nil},
		{"svi", OpWrite, `D:\System Volume Information\x`, alice, ErrProtected},
		{"svi read", OpRead, `D:\system volume information`, alice, ErrProtected},
		{"recycle bin", OpDelete, `D:\$Recycle.Bin\S-1-5-21\x`, alice, ErrProtected},
		{"pagefile read", OpRead, `C:\pagefile.sys`, alice, ErrProtected},
		{"hiberfil write", OpWrite, `C:\hiberfil.sys`, alice, ErrProtected},
		{"swapfile delete", OpDelete, `E:\swapfile.sys`, alice, ErrProtected},
		{"bootmgr write", OpWrite, `C:\bootmgr`, alice, ErrProtected},
		{"bootmgr read", OpRead, `C:\bootmgr`, alice, nil},
		{"recovery write", OpWrite, `C:\Recovery\x`, alice, ErrProtected},

		// UNC.
		{"unc share root", OpWrite, `\\server\share`, alice, ErrProtected},
		{"unc svi", OpWrite, `\\?\UNC\server\share\System Volume Information\x`, alice, ErrProtected},
		{"admin share", OpRead, `\\localhost\C$\Windows\x`, alice, ErrProtected},
		{"admin share root", OpRead, `\\localhost\ADMIN$`, alice, ErrProtected},
		{"unc server only", OpWrite, `\\server`, alice, ErrInvalidPath},

		// Malformed, device and stream paths.
		{"ads", OpWrite, `C:\Users\alice\a.txt:evil`, alice, ErrInvalidPath},
		{"ads data", OpRead, `C:\Users\alice\a.txt::$DATA`, alice, ErrInvalidPath},
		{"ads on dir", OpWrite, `C:\Users\alice\docs:x\a.txt`, alice, ErrInvalidPath},
		{"relative", OpWrite, `Users\alice\a.txt`, alice, ErrInvalidPath},
		{"drive relative", OpWrite, `C:a.txt`, alice, ErrInvalidPath},
		{"rooted no drive", OpWrite, `\Windows\x`, alice, ErrInvalidPath},
		{"empty", OpRead, ``, alice, ErrInvalidPath},
		{"nul byte", OpRead, "C:\\Users\\alice\\a\x00b", alice, ErrInvalidPath},
		{"wildcard", OpWrite, `C:\Users\alice\*.txt`, alice, ErrInvalidPath},
		{"device", OpWrite, `\\.\PhysicalDrive0`, alice, ErrInvalidPath},
		{"globalroot", OpWrite, `\\?\GLOBALROOT\Device\HarddiskVolumeShadowCopy1\x`, alice, ErrInvalidPath},
		{"nt prefix", OpWrite, `\??\C:\Users\alice\x`, alice, ErrInvalidPath},
		{"volume guid", OpWrite, `\\?\Volume{12345678-1234-1234-1234-123456789abc}\x`, alice, ErrInvalidPath},
		{"verbatim trailing dot", OpWrite, `\\?\C:\Windows.\x`, alice, ErrInvalidPath},
		{"escapes root", OpWrite, `C:\..\Windows\x`, alice, ErrInvalidPath},
		{"only dots", OpWrite, `C:\Users\alice\...\x`, alice, ErrInvalidPath},
		{"reserved con", OpWrite, `C:\Users\alice\CON`, alice, ErrInvalidPath},
		{"reserved nul ext", OpWrite, `C:\Users\alice\nul.txt`, alice, ErrInvalidPath},
		{"reserved com1", OpWrite, `C:\Users\alice\com1 .log`, alice, ErrInvalidPath},
		{"not reserved", OpWrite, `C:\Users\alice\console.txt`, alice, nil},

		{"unknown op", Op(0), `C:\Users\alice\a.txt`, alice, ErrProtected},
		{"unknown op high", Op(9), `C:\Users\alice\a.txt`, alice, ErrProtected},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := g.Check(tt.op, tt.path, tt.prof)
			if tt.want == nil {
				if err != nil {
					t.Fatalf("Check(%s, %q) = %v, want nil", tt.op, tt.path, err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("Check(%s, %q) = %v, want %v", tt.op, tt.path, err, tt.want)
			}
			var be *BlockedError
			if !errors.As(err, &be) || be.Op != tt.op || be.Path != tt.path || be.Reason == "" {
				t.Fatalf("error %#v is not a complete *BlockedError", err)
			}
		})
	}
}

func TestLexical(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{`C:\Users\alice\a.txt`, `C:\Users\alice\a.txt`},
		{`c:/users//alice/./b/../a.txt`, `C:\users\alice\a.txt`},
		{`C:\dir. .\file.txt. `, `C:\dir\file.txt`},
		{`C:`, `C:\`},
		{`C:\`, `C:\`},
		{`\\?\D:\x`, `D:\x`},
		{`\\?\D:`, `D:\`},
		{`\\server\share`, `\\server\share`},
		{`\\server\share\`, `\\server\share`},
		{`\\server\share\a\..\b`, `\\server\share\b`},
		{`\\?\UNC\server\share\x`, `\\server\share\x`},
		{`//server/share/x`, `\\server\share\x`},
	}
	for _, tt := range tests {
		c, err := lexical(tt.in)
		if err != nil {
			t.Errorf("lexical(%q): %v", tt.in, err)
			continue
		}
		if got := c.String(); got != tt.want {
			t.Errorf("lexical(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	for _, bad := range []string{`\\server\share\..\..\x`, `\\?\UNC\server`, `\\server\.\x`, `\\srv\sh:are\x`} {
		if _, err := lexical(bad); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("lexical(%q) err = %v, want ErrInvalidPath", bad, err)
		}
	}
}

func TestCanonicalizeRejects(t *testing.T) {
	if _, err := Canonicalize(`rel\path`); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("err = %v", err)
	}
	got, err := Canonicalize(`Z:\no\such\dir\..\file.`)
	if err != nil || got != `Z:\no\such\file` {
		t.Fatalf("Canonicalize = %q, %v", got, err)
	}
}

func TestNewRejectsBadEnv(t *testing.T) {
	if _, err := New(Env{WinDir: `Windows`}); err == nil {
		t.Fatal("relative env path accepted")
	}
}

func TestOpString(t *testing.T) {
	for op, want := range map[Op]string{OpRead: "read", OpWrite: "write", OpDelete: "delete", OpRestoreWrite: "restore-write", Op(7): "op(7)"} {
		if op.String() != want {
			t.Errorf("%d.String() = %q", op, op.String())
		}
	}
}

func TestDefaultCheck(t *testing.T) {
	// The package-level Check must fail closed for invalid input whatever
	// the machine environment.
	if err := Check(OpWrite, `relative`, Profile{SID: "S-1"}); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("Check = %v", err)
	}
}
