package modules

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/client"
)

// NewForHost builds the Manager the agent Host owns, in one call:
//   - db is state.Store.DB (migration 5 tables);
//   - modeFile is the installer's ProgramData config\mode.json;
//   - cl returns the current enrolled server client (nil while not
//     connected) and backs the PUT /device/modules sender;
//   - publish receives every ModulesState change (wrap Bus.Publish with
//     the modules event kind).
//
// The Host must still start the report loop (go m.RunReporter(ctx)), route
// mode_change_request tasks to HandleTask and register item sources. It
// returns nil (and logs) only when state.db cannot be read; the Host should
// then treat modules as unavailable.
func NewForHost(db *sql.DB, modeFile string, coreVersion string, cl func() *client.Client, publish func(any)) *Manager {
	opts := Options{
		DB:          db,
		ModeFile:    modeFile,
		CoreVersion: coreVersion,
		Send:        ClientSender(cl),
	}
	if publish != nil {
		opts.OnChange = func(s ModulesState) { publish(s) }
	}
	m, err := New(context.Background(), opts)
	if err != nil {
		slog.Error("module manager unavailable", "component", "modules", "err", err)
		return nil
	}
	return m
}
