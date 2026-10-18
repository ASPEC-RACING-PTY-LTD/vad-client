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

	out := filepath.Join(root, "build", "windows", "installer", "payload", arch, "vaultdock-agent.exe")
	cmd := exec.Command("go", "build", "-trimpath",
		"-ldflags", "-s -w -X main.version="+version,
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
