package modules

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/config"
	sdkmod "github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk/modules"
)

// RegisterSource adds an ItemSource (backup jobs, sync folders, automations,
// profiles) whose items are suspended and resumed by mode and module
// changes. It is safe to call at any time.
func (m *Manager) RegisterSource(src ItemSource) {
	if src == nil {
		return
	}
	m.srcMu.Lock()
	defer m.srcMu.Unlock()
	m.srcs = append(m.srcs, src)
}

func (m *Manager) sources() []ItemSource {
	m.srcMu.Lock()
	defer m.srcMu.Unlock()
	return slices.Clone(m.srcs)
}

// ItemKindSyncFolder is AffectedItem.Kind for a sync folder.
const ItemKindSyncFolder = "sync_folder"

// suspendedSyncKey is the state.db kv key holding the ids of sync folders
// this package disabled (JSON array), so Resume only re-enables those and
// never a folder the user turned off.
const suspendedSyncKey = "modules.suspended_sync_folders"

// SyncConfigStore is the part of *engine.Engine the sync folder source
// needs. SaveConfig must persist and restart or stop the folder's watcher.
type SyncConfigStore interface {
	Configs() []config.SyncConfig
	SaveConfig(c config.SyncConfig) (config.SyncConfig, error)
}

// SyncFolderSource adapts the existing sync configurations to ItemSource.
// config.SyncConfig has no suspended_by_mode state yet, so suspending sets
// Enabled=false and records the id in the kv table; nothing is deleted and
// no files on either side are touched. Resume re-enables recorded folders
// whose required modules are all enabled again.
type SyncFolderSource struct {
	db       *sql.DB
	store    SyncConfigStore
	requires func(config.SyncConfig) []sdkmod.ID
	mu       sync.Mutex
}

// NewSyncFolderSource returns a sync folder source. requires maps a folder
// to the modules it needs; nil = every folder needs sync (one-way sync is
// part of the sync module, api-v1 §6A).
func NewSyncFolderSource(db *sql.DB, store SyncConfigStore, requires func(config.SyncConfig) []sdkmod.ID) *SyncFolderSource {
	if requires == nil {
		requires = func(config.SyncConfig) []sdkmod.ID { return []sdkmod.ID{sdkmod.Sync} }
	}
	return &SyncFolderSource{db: db, store: store, requires: requires}
}

// Affected lists enabled folders that need any of ids.
func (s *SyncFolderSource) Affected(_ context.Context, ids []sdkmod.ID) ([]AffectedItem, error) {
	var out []AffectedItem
	for _, c := range s.store.Configs() {
		if !c.Enabled {
			continue
		}
		req := s.requires(c)
		if !slices.ContainsFunc(req, func(id sdkmod.ID) bool { return slices.Contains(ids, id) }) {
			continue
		}
		out = append(out, AffectedItem{Kind: ItemKindSyncFolder, ID: c.ID, Name: c.Name, Requires: req,
			Reason: "sync stops; no files are deleted"})
	}
	slices.SortFunc(out, func(a, b AffectedItem) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// Suspend disables the given folders and records them.
func (s *SyncFolderSource) Suspend(ctx context.Context, items []AffectedItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.load(ctx)
	if err != nil {
		return err
	}
	byID := map[string]config.SyncConfig{}
	for _, c := range s.store.Configs() {
		byID[c.ID] = c
	}
	var errs []error
	for _, it := range items {
		if it.Kind != ItemKindSyncFolder {
			continue
		}
		c, ok := byID[it.ID]
		if !ok || !c.Enabled {
			continue
		}
		// Record first: a crash between the two leaves a recorded, still
		// enabled folder, which Resume handles.
		if !slices.Contains(rec, c.ID) {
			rec = append(rec, c.ID)
			if err := s.save(ctx, rec); err != nil {
				return err
			}
		}
		c.Enabled = false
		if _, err := s.store.SaveConfig(c); err != nil {
			errs = append(errs, fmt.Errorf("suspend sync folder %s: %w", c.ID, err))
		}
	}
	return errors.Join(errs...)
}

// Resume re-enables recorded folders whose modules are all enabled.
func (s *SyncFolderSource) Resume(ctx context.Context, enabled []sdkmod.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.load(ctx)
	if err != nil || len(rec) == 0 {
		return err
	}
	byID := map[string]config.SyncConfig{}
	for _, c := range s.store.Configs() {
		byID[c.ID] = c
	}
	var keep []string
	var errs []error
	for _, id := range rec {
		c, ok := byID[id]
		if !ok {
			continue // deleted by the user meanwhile; forget it
		}
		if !allIn(s.requires(c), enabled) {
			keep = append(keep, id)
			continue
		}
		if !c.Enabled {
			c.Enabled = true
			if _, err := s.store.SaveConfig(c); err != nil {
				errs = append(errs, fmt.Errorf("resume sync folder %s: %w", id, err))
				keep = append(keep, id)
			}
		}
	}
	if err := s.save(ctx, keep); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Suspended returns the ids of folders currently suspended by mode.
func (s *SyncFolderSource) Suspended(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load(ctx)
}

func (s *SyncFolderSource) load(ctx context.Context) ([]string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, "SELECT v FROM kv WHERE k = ?", suspendedSyncKey).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && v == "") {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("modules: load suspended folders: %w", err)
	}
	var ids []string
	if err := json.Unmarshal([]byte(v), &ids); err != nil {
		return nil, fmt.Errorf("modules: parse suspended folders: %w", err)
	}
	return ids, nil
}

func (s *SyncFolderSource) save(ctx context.Context, ids []string) error {
	if ids == nil {
		ids = []string{}
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO kv(k, v) VALUES(?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v", suspendedSyncKey, string(raw))
	if err != nil {
		return fmt.Errorf("modules: save suspended folders: %w", err)
	}
	return nil
}

func allIn(need, have []sdkmod.ID) bool {
	for _, id := range need {
		if !slices.Contains(have, id) {
			return false
		}
	}
	return true
}
