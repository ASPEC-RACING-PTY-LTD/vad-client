//go:build windows

package sysload

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// gameWatch finds running games. Processes are listed in every session (the
// service sees the user's games too); paths are cached per process.
type gameWatch struct {
	mu      sync.Mutex
	paths   map[uint32]string
	extra   []string
	extraAt time.Time
}

var games gameWatch

// runningGame returns the name of a running game, or "".
func runningGame() string {
	return games.find()
}

func (g *gameWatch) find() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.paths == nil {
		g.paths = map[uint32]string{}
	}
	if time.Since(g.extraAt) > 10*time.Minute {
		g.extra, g.extraAt = libraryFolders(), time.Now()
	}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	seen := map[uint32]bool{}
	found := ""
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		pid := pe.ProcessID
		if pid <= 4 {
			continue
		}
		seen[pid] = true
		p, ok := g.paths[pid]
		if !ok {
			p = imagePath(pid)
			g.paths[pid] = p
		}
		if found == "" {
			found = GameName(p, g.extra)
		}
	}
	for pid := range g.paths {
		if !seen[pid] {
			delete(g.paths, pid)
		}
	}
	return found
}

func imagePath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// libraryFolders reads custom install folders from the launchers: Epic Games
// manifests, Ubisoft Connect and GOG Galaxy registry entries.
func libraryFolders() []string {
	var out []string
	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	items, _ := filepath.Glob(filepath.Join(pd, `Epic\EpicGamesLauncher\Data\Manifests\*.item`))
	for _, it := range items {
		b, err := os.ReadFile(it)
		if err != nil {
			continue
		}
		var m struct {
			InstallLocation string `json:"InstallLocation"`
		}
		if json.Unmarshal(b, &m) == nil && m.InstallLocation != "" {
			out = append(out, filepath.Dir(m.InstallLocation))
		}
	}
	out = append(out, registryDirs(`SOFTWARE\WOW6432Node\Ubisoft\Launcher\Installs`, "InstallDir", true)...)
	out = append(out, registryDirs(`SOFTWARE\WOW6432Node\GOG.com\Games`, "path", true)...)
	return out
}

// registryDirs reads value from every subkey of key (HKLM); parent returns
// the folder that holds each install.
func registryDirs(key, value string, parent bool) []string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, key, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()
	names, _ := k.ReadSubKeyNames(-1)
	var out []string
	for _, n := range names {
		sk, err := registry.OpenKey(k, n, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		v, _, err := sk.GetStringValue(value)
		sk.Close()
		if err != nil || v == "" {
			continue
		}
		v = filepath.Clean(v)
		if parent {
			v = filepath.Dir(v)
		}
		out = append(out, v)
	}
	return out
}
