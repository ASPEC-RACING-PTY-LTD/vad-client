// Command vaultdock-restore restores files from a directory copy of a
// VaultDock repository with only the recovery key (or a passphrase), with
// no server (T-R3, P2-D4). Writes go through system-file protection (guard)
// into the current user's profile or a chosen folder; existing files are
// never overwritten in place (default policy rename).
//
//	vaultdock-restore -repo D:\copy list
//	vaultdock-restore -repo D:\copy ls <snapshot> [token path]
//	vaultdock-restore -repo D:\copy restore -to C:\Users\me\Restored [-policy rename|skip|overwrite] <snapshot> [token path ...]
//
// The key is read from VAULTDOCK_RECOVERY_KEY or prompted on stdin; it is
// never stored.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/guard"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/restore"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/repo"
	sdkrestore "github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/restore"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/snapshot"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/wire"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "vaultdock-restore:", err)
		os.Exit(1)
	}
}

func run() error {
	repoDir := flag.String("repo", "", "directory copy of the repository")
	pass := flag.Bool("passphrase", false, "open with a passphrase instead of the recovery key")
	flag.Parse()
	args := flag.Args()
	if *repoDir == "" || len(args) == 0 {
		flag.Usage()
		return errors.New("need -repo and a command (list, ls, restore)")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	be, err := repo.NewDirBackend(*repoDir)
	if err != nil {
		return err
	}
	cred, err := credential(*pass)
	if err != nil {
		return err
	}
	r, err := repo.Open(ctx, be, cred, repo.Options{})
	if err != nil {
		return fmt.Errorf("open repository: %w", err)
	}
	defer r.Close()

	switch args[0] {
	case "list":
		return list(ctx, r)
	case "ls":
		if len(args) < 2 {
			return errors.New("ls <snapshot> [token path]")
		}
		p := ""
		if len(args) > 2 {
			p = args[2]
		}
		return ls(ctx, r, args[1], p)
	case "restore":
		return restoreCmd(ctx, r, args[1:])
	}
	return fmt.Errorf("unknown command %q", args[0])
}

func credential(pass bool) (repo.Credential, error) {
	if !pass {
		if k := strings.TrimSpace(os.Getenv("VAULTDOCK_RECOVERY_KEY")); k != "" {
			return repo.RecoveryCredential(k)
		}
	}
	prompt := "Recovery key (VDRK1-…): "
	if pass {
		prompt = "Passphrase: "
	}
	fmt.Fprint(os.Stderr, prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return repo.Credential{}, err
	}
	line = strings.TrimRight(line, "\r\n")
	if pass {
		return repo.PassphraseCredential([]byte(line)), nil
	}
	return repo.RecoveryCredential(strings.TrimSpace(line))
}

func load(ctx context.Context, r *repo.Repo, oid string) (*snapshot.Root, error) {
	root, stored, err := r.LoadSnapshot(ctx, oid)
	if err != nil {
		return nil, err
	}
	if err := r.VerifySnapshot(root, stored); err != nil {
		if !errors.Is(err, repo.ErrNoDeviceKey) {
			return nil, fmt.Errorf("snapshot signature: %w", err)
		}
		fmt.Fprintln(os.Stderr, "warning: the snapshot's device key is not in the repository; signature not checked")
	}
	return root, nil
}

func list(ctx context.Context, r *repo.Repo) error {
	oids, err := r.ListSnapshots(ctx)
	if err != nil {
		return err
	}
	type row struct {
		oid  string
		root *snapshot.Root
	}
	var rows []row
	for _, oid := range oids {
		root, _, err := r.LoadSnapshot(ctx, oid)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", oid, err)
			continue
		}
		rows = append(rows, row{oid, root})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].root.Time > rows[j].root.Time })
	for _, x := range rows {
		var roots []string
		for _, rr := range x.root.Roots {
			roots = append(roots, rr.TokenPath)
		}
		fmt.Printf("%s  %s  %s  %s  %s\n", x.oid, time.UnixMilli(x.root.Time).Format(time.DateTime),
			x.root.Host.Hostname, x.root.Job.Name, strings.Join(roots, ","))
	}
	return nil
}

func ls(ctx context.Context, r *repo.Repo, oid, p string) error {
	root, err := load(ctx, r, oid)
	if err != nil {
		return err
	}
	if p == "" {
		for _, rr := range root.Roots {
			fmt.Println(rr.TokenPath + "/")
		}
		return nil
	}
	items, err := sdkrestore.ListDir(ctx, r, root.Roots, p)
	if err != nil {
		return err
	}
	for _, it := range items {
		name := it.Path
		if it.Entry.T == snapshot.TypeDir {
			name += "/"
		}
		fmt.Printf("%12d  %s\n", it.Entry.S, name)
	}
	return nil
}

func restoreCmd(ctx context.Context, r *repo.Repo, args []string) error {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	to := fs.String("to", "", "folder to restore into (required)")
	policy := fs.String("policy", wire.RestoreConflictRename, "conflict policy: rename, skip or overwrite (the old file is kept)")
	_ = fs.Parse(args)
	rest := fs.Args()
	if *to == "" || len(rest) == 0 {
		return errors.New("restore -to <folder> [-policy p] <snapshot> [token path ...]")
	}
	if !sdkrestore.ValidPolicy(*policy) {
		return fmt.Errorf("unknown policy %q", *policy)
	}
	target, err := filepath.Abs(*to)
	if err != nil {
		return err
	}
	root, err := load(ctx, r, rest[0])
	if err != nil {
		return err
	}
	var sel []wire.RestoreSelection
	for _, p := range rest[1:] {
		sel = append(sel, wire.RestoreSelection{Path: p})
	}
	// Every root goes under <target>\<token name>, never the original
	// locations.
	var overrides []wire.RestorePathMapping
	for _, rr := range root.Roots {
		overrides = append(overrides, wire.RestorePathMapping{From: rr.TokenPath, To: filepath.Join(target, folderName(rr.TokenPath))})
	}
	u, err := user.Current()
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	prof := guard.Profile{SID: u.Uid, Root: home}
	g, err := guard.New(guard.SystemEnv())
	if err != nil {
		return err
	}
	var last time.Time
	p, err := restore.Run(ctx, r, restore.Request{Roots: root.Roots, Selection: sel, Overrides: overrides,
		Tokens: snapshot.Tokens{}, Policy: *policy, Profile: prof}, restore.Options{Guard: g.Check,
		OnItem: func(it wire.RestoreItem) {
			if it.Status == wire.RestoreItemFailed || it.Status == wire.RestoreItemBlocked {
				fmt.Fprintf(os.Stderr, "%s: %s: %s\n", it.Status, it.Path, it.Message)
			}
		},
		OnProgress: func(p restore.Progress) {
			if time.Since(last) > time.Second {
				last = time.Now()
				fmt.Fprintf(os.Stderr, "\r%d/%d files", p.FilesDone, p.FilesTotal)
			}
		}})
	fmt.Fprintf(os.Stderr, "\r%d/%d files, %d skipped, %d failed, %d blocked: %s\n",
		p.FilesDone, p.FilesTotal, p.Skipped, p.Failed, p.Blocked, p.Status())
	return err
}

// folderName is the folder a token root restores into: {DOCUMENTS}/x →
// Documents\x.
func folderName(tokenPath string) string {
	tok, comps, err := snapshot.SplitTokenPath(tokenPath)
	if err != nil {
		return "Restored"
	}
	t := strings.Trim(tok, "{}")
	if strings.HasPrefix(t, "VOL:") && len(t) >= 12 {
		t = "Volume-" + t[4:12]
	} else if len(t) > 1 {
		t = t[:1] + strings.ToLower(t[1:])
	}
	return filepath.Join(append([]string{t}, comps...)...)
}
