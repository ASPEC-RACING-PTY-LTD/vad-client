//go:build ignore

// Command build-agent builds vaultdock-agent.exe (desktop/cmd/vaultdock-agent)
// into build/windows/installer/payload/<arch>/ so the NSIS installer can
// ship it next to the Wails UI binary (Wails itself only packs the UI).
//
// It is run by the wails.json pre-build hook ("windows/*") as
//
//	go run ../windows/installer/build-agent.go windows/amd64
//
// (Wails runs hooks with build/bin as the working directory). It can also be
// run by hand from anywhere inside the desktop module.
//
// The agent version comes from $VAULTDOCK_VERSION when set (release builds
// should set it to the full tag version), otherwise from wails.json
// info.productVersion.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tc-hib/winres"
	wrversion "github.com/tc-hib/winres/version"
)

const desktopModule = "module github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop"

var versionRE = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]{0,63}$`)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "build-agent:", err)
		os.Exit(1)
	}
}

func run() error {
	platform := "windows/amd64"
	if len(os.Args) > 1 {
		platform = os.Args[1]
	}
	goos, arch, ok := strings.Cut(platform, "/")
	if !ok || goos != "windows" || (arch != "amd64" && arch != "arm64") {
		return fmt.Errorf("unsupported platform %q (windows/amd64 or windows/arm64)", platform)
	}

	root, err := desktopRoot()
	if err != nil {
		return err
	}
	version := os.Getenv("VAULTDOCK_VERSION")
	if version == "" {
		if version, err = wailsVersion(filepath.Join(root, "wails.json")); err != nil {
			return err
		}
	}
	version = strings.TrimPrefix(version, "v")
	if !versionRE.MatchString(version) {
		return fmt.Errorf("invalid version %q", version)
	}

	// Version information, icon and manifest: Windows and antivirus
	// products show and weigh who made a program and what it is; a service
	// binary without them looks like an anonymous dropper.
	syso := filepath.Join(root, "cmd", "vaultdock-agent", "rsrc_windows_"+arch+".syso")
	if err := writeResources(root, syso, arch, version); err != nil {
		return fmt.Errorf("agent resources: %w", err)
	}
	defer os.Remove(syso)

	out := filepath.Join(root, "build", "windows", "installer", "payload", arch, "vaultdock-agent.exe")
	// Symbols are kept (no -s -w): stripped binaries are a common heuristic
	// signal for antivirus scanners.
	cmd := exec.Command("go", "build", "-trimpath",
		"-ldflags", "-X main.version="+version,
		"-o", out, "./cmd/vaultdock-agent")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH="+arch)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build ./cmd/vaultdock-agent: %w", err)
	}
	fmt.Printf("built %s (version %s)\n", out, version)
	return nil
}

// writeResources writes the agent's Windows resources (version information,
// icon, asInvoker manifest) as a .syso object that go build links in.
func writeResources(root, out, arch, ver string) error {
	rs := &winres.ResourceSet{}
	f, err := os.Open(filepath.Join(root, "build", "windows", "icon.ico"))
	if err != nil {
		return err
	}
	icon, err := winres.LoadICO(f)
	f.Close()
	if err != nil {
		return err
	}
	if err := rs.SetIcon(winres.Name("APPICON"), icon); err != nil {
		return err
	}
	vi := wrversion.Info{}
	vi.SetFileVersion(ver)
	vi.SetProductVersion(ver)
	for k, v := range map[string]string{
		wrversion.CompanyName:      "ASPEC Racing Pty Ltd",
		wrversion.ProductName:      "VaultDock",
		wrversion.FileDescription:  "VaultDock background service (backup and sync agent)",
		wrversion.InternalName:     "vaultdock-agent",
		wrversion.OriginalFilename: "vaultdock-agent.exe",
		wrversion.LegalCopyright:   "Copyright 2026 ASPEC Racing Pty Ltd",
		wrversion.Comments:         "Self-hosted file storage and synchronisation",
		wrversion.ProductVersion:   ver,
		wrversion.FileVersion:      ver,
	} {
		if err := vi.Set(wrversion.LangDefault, k, v); err != nil {
			return err
		}
	}
	rs.SetVersionInfo(vi)
	rs.SetManifest(winres.AppManifest{
		Identity:       winres.AssemblyIdentity{Name: "VaultDock.Agent"},
		Description:    "VaultDock background service",
		ExecutionLevel: winres.AsInvoker,
		Compatibility:  winres.Win10AndAbove,
		LongPathAware:  true,
	})
	w, err := os.Create(out)
	if err != nil {
		return err
	}
	a := winres.ArchAMD64
	if arch == "arm64" {
		a = winres.ArchARM64
	}
	if err := rs.WriteObject(w, a); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

// desktopRoot walks up from the working directory to the desktop module.
func desktopRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		raw, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && isDesktopModule(raw) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("desktop module (go.mod) not found above the working directory")
		}
		dir = parent
	}
}

func isDesktopModule(gomod []byte) bool {
	for _, line := range strings.Split(string(gomod), "\n") {
		if strings.TrimSpace(line) == desktopModule {
			return true
		}
	}
	return false
}

func wailsVersion(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var cfg struct {
		Info struct {
			ProductVersion string `json:"productVersion"`
		} `json:"info"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Info.ProductVersion == "" {
		return "", fmt.Errorf("%s: info.productVersion is empty", path)
	}
	return cfg.Info.ProductVersion, nil
}
