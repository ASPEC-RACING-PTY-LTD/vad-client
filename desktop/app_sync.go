package main

import (
	"os"
	"path/filepath"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/agent"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/config"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/engine"
	"github.com/ASPEC-RACING-PTY-LTD/vad-client/desktop/internal/state"
)

// --- sync configurations (agent) ---

func (a *App) ListConfigs() ([]engine.ConfigView, error) { return a.api.ListConfigs(a.bg) }

func (a *App) SaveConfig(c config.SyncConfig) (config.SyncConfig, error) {
	return a.api.SaveConfig(a.bg, c)
}

func (a *App) DeleteConfig(id string) error { return a.api.DeleteConfig(a.bg, id) }

// SyncNow scans one configuration, or every enabled one when id is "".
func (a *App) SyncNow(id string) error { return a.api.SyncNow(a.bg, id) }

func (a *App) PreviewConfig(c config.SyncConfig) (agent.PreviewResult, error) {
	return a.api.PreviewConfig(a.bg, c)
}

// --- pickers and suggestions (UI session) ---

func (a *App) PickFolder(title string) (string, error) {
	return wruntime.OpenDirectoryDialog(a.wctx(), wruntime.OpenDialogOptions{Title: title})
}

func (a *App) PickFiles() ([]string, error) {
	return wruntime.OpenMultipleFilesDialog(a.wctx(), wruntime.OpenDialogOptions{Title: "Choose files to synchronise"})
}

// SuggestFolders returns existing well-known folders (Medal, screenshots) of
// the signed-in user.
func (a *App) SuggestFolders() map[string]string {
	home, _ := os.UserHomeDir()
	out := map[string]string{}
	candidates := map[string][]string{
		"medal":       {filepath.Join(home, "Videos", "Medal", "Clips"), filepath.Join(home, "Videos", "Medal"), filepath.Join(home, "Medal", "Clips"), `C:\Medal\Clips`, `D:\Medal\Clips`},
		"screenshots": {filepath.Join(home, "Pictures", "Screenshots")},
		"documents":   {filepath.Join(home, "Documents")},
		"videos":      {filepath.Join(home, "Videos")},
	}
	for k, list := range candidates {
		for _, p := range list {
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				out[k] = p
				break
			}
		}
	}
	return out
}

// --- transfers (agent) ---

func (a *App) GetSnapshot() (engine.Snapshot, error) { return a.api.Snapshot(a.bg) }

func (a *App) SetPaused(p bool) error { return uiError(a.api.SetPaused(a.bg, p)) }

// ResetSyncData forgets what was uploaded and rescans, so everything is
// uploaded again (for example after the server was set up fresh).
func (a *App) ResetSyncData() (int64, error) { return a.api.ResetSyncData(a.bg) }

// RetryFailed requeues one failed item, or all of them when id is 0.
func (a *App) RetryFailed(id int64) (int64, error) { return a.api.RetryFailed(a.bg, id) }

func (a *App) ListQueue(status string) ([]state.Item, error) { return a.api.ListQueue(a.bg, status) }

func (a *App) ListHistory(status string) ([]state.HistoryEntry, error) {
	return a.api.ListHistory(a.bg, status)
}

// --- automations (agent) ---

func (a *App) ListAutomations() ([]engine.AutomationView, error) {
	return a.api.ListAutomations(a.bg)
}

func (a *App) RefreshAutomations() ([]engine.AutomationView, error) {
	return a.api.RefreshAutomations(a.bg)
}
