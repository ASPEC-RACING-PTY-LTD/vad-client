//go:build windows

// Command vss-spike is the P0-S1 prototype: WMI Win32_ShadowCopy lifecycle
// plus AccessCheck-gated snapshot reads under a user token.
//
//	vss-spike check <path>                  non-admin: AccessCheck(GENERIC_READ) of own token on path
//	vss-spike create <volume>               admin: create shadow, print ID and device
//	vss-spike delete <shadowID>             admin: delete one shadow
//	vss-spike read <shadowID> <live> <out>  SYSTEM: gated copy of live's snapshot twin to out
//	vss-spike demo <live> <out>             SYSTEM: create -> gated read -> delete
//
// When run as SYSTEM (e.g. psexec -s in a VM), the user token is the active
// console session's (WTSQueryUserToken); otherwise the process token is used.
package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("vss-spike", "err", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: vss-spike check|create|delete|read|demo ...")
	}
	switch args[0] {
	case "check":
		g, err := userGate()
		if err != nil {
			return err
		}
		defer g.Close()
		err = g.CheckLive(args[1])
		fmt.Printf("%s: allowed=%v err=%v\n", args[1], err == nil, err)
		return nil
	case "create":
		w, err := connectWMI()
		if err != nil {
			return err
		}
		defer w.Close()
		id, err := w.CreateShadow(args[1])
		if err != nil {
			return err
		}
		dev, err := w.DeviceObject(id)
		fmt.Printf("id=%s device=%s err=%v\n", id, dev, err)
		return nil
	case "delete":
		w, err := connectWMI()
		if err != nil {
			return err
		}
		defer w.Close()
		return w.DeleteShadow(args[1])
	case "read":
		if len(args) != 4 {
			return errors.New("usage: read <shadowID> <live> <out>")
		}
		w, err := connectWMI()
		if err != nil {
			return err
		}
		dev, err := w.DeviceObject(args[1])
		w.Close()
		if err != nil {
			return err
		}
		return gatedCopy(dev, args[2], args[3])
	case "demo":
		if len(args) != 3 {
			return errors.New("usage: demo <live> <out>")
		}
		return demo(args[1], args[2])
	}
	return fmt.Errorf("unknown command %q", args[0])
}

func demo(live, out string) error {
	vol := filepath.VolumeName(live) + `\`
	w, err := connectWMI()
	if err != nil {
		return err
	}
	defer w.Close()
	id, err := w.CreateShadow(vol)
	if err != nil {
		return err
	}
	slog.Info("shadow created", "id", id) // production: persist id before continuing
	defer func() {
		if err := w.DeleteShadow(id); err != nil {
			slog.Error("shadow delete failed", "id", id, "err", err)
		} else {
			slog.Info("shadow deleted", "id", id)
		}
	}()
	dev, err := w.DeviceObject(id)
	if err != nil {
		return err
	}
	return gatedCopy(dev, live, out)
}

// snapshotPath maps C:\Users\x\f onto \\?\GLOBALROOT\Device\HarddiskVolumeShadowCopyN\Users\x\f.
func snapshotPath(device, live string) (string, error) {
	abs, err := filepath.Abs(live)
	if err != nil {
		return "", err
	}
	vol := filepath.VolumeName(abs)
	if len(vol) != 2 || vol[1] != ':' {
		return "", fmt.Errorf("%s: only drive-letter paths are supported", live)
	}
	return strings.TrimRight(device, `\`) + abs[len(vol):], nil
}

func gatedCopy(device, live, out string) error {
	snap, err := snapshotPath(device, live)
	if err != nil {
		return err
	}
	g, err := userGate()
	if err != nil {
		return err
	}
	defer g.Close()
	var f *os.File
	err = withBackupPrivilege(func() error {
		var err error
		f, err = g.OpenSnapshotFile(live, snap)
		return err
	})
	if errors.Is(err, ErrDenied) {
		slog.Warn("skipped: user may not read file", "path", live) // production: issue record
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	dst, err := os.Create(out)
	if err != nil {
		return err
	}
	n, err := io.Copy(dst, f)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	slog.Info("copied from snapshot", "path", live, "bytes", n)
	return err
}

// userGate builds a Gate for the active console user when running as
// LocalSystem, or for the current process token otherwise.
func userGate() (*Gate, error) {
	var tok windows.Token
	self := windows.GetCurrentProcessToken()
	u, err := self.GetTokenUser()
	if err != nil {
		return nil, err
	}
	if u.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		sess := windows.WTSGetActiveConsoleSessionId()
		if err := windows.WTSQueryUserToken(sess, &tok); err != nil {
			return nil, fmt.Errorf("WTSQueryUserToken(%d): %w", sess, err)
		}
		defer tok.Close()
	} else if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY, &tok); err != nil {
		return nil, err
	} else {
		defer tok.Close()
	}
	return NewGate(tok)
}
