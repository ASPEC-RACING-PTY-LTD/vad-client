package state

import (
	"database/sql"

	"github.com/ASPEC-RACING-PTY-LTD/VaultDock/desktop/internal/dbutil"
)

// legacyTables are the tables of the pre-migration schema (now migration 1).
var legacyTables = []string{"configs", "files", "queue", "history", "kv"}

// migrations is the ordered state.db schema. Append only; never edit a
// released step. Timestamps are unix milliseconds unless noted.
var migrations = []dbutil.Migration{
	{Version: 1, Name: "base", SQL: schemaV1},
	// Scan/transfer/backup issues surfaced to the user and the server (B12).
	// class: locked, permission_denied, not_found, too_long, io_error,
	// profile_unauthorised, module_unavailable, guard_blocked, …
	// state: open | skipped | resolved.
	{Version: 2, Name: "issues", SQL: `
CREATE TABLE issues (
	id INTEGER PRIMARY KEY, source TEXT NOT NULL, config_id TEXT NOT NULL DEFAULT '', run_id TEXT NOT NULL DEFAULT '',
	rel_path TEXT NOT NULL DEFAULT '', class TEXT NOT NULL, message TEXT NOT NULL DEFAULT '',
	count INTEGER NOT NULL DEFAULT 1, state TEXT NOT NULL DEFAULT 'open',
	first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL, reported_at INTEGER,
	UNIQUE (source, config_id, rel_path, class)
);
CREATE INDEX issues_state ON issues(state, last_seen);
CREATE INDEX issues_unreported ON issues(reported_at) WHERE reported_at IS NULL;`},
	// Server reports that could not be delivered yet; drained in id order.
	// dedupe_key (optional) lets a newer report replace a pending one.
	{Version: 3, Name: "outbox", SQL: `
CREATE TABLE outbox (
	id INTEGER PRIMARY KEY, kind TEXT NOT NULL, method TEXT NOT NULL, path TEXT NOT NULL, body BLOB,
	dedupe_key TEXT UNIQUE, attempts INTEGER NOT NULL DEFAULT 0, next_at INTEGER NOT NULL,
	last_error TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL
);
CREATE INDEX outbox_ready ON outbox(next_at, id);`},
	// Last fire time of schedule triggers, keyed "<automation id>/<node id>" (B3).
	{Version: 4, Name: "schedule_last", SQL: `
CREATE TABLE schedule_last (key TEXT PRIMARY KEY, last_fire INTEGER NOT NULL);`},
	// Install mode (single row) and module states (api-v1 §6A).
	{Version: 5, Name: "modules", SQL: `
CREATE TABLE install_mode (
	id INTEGER PRIMARY KEY CHECK (id = 1), mode TEXT NOT NULL, core_version TEXT NOT NULL DEFAULT '',
	changed_at INTEGER NOT NULL, reported_at INTEGER
);
CREATE TABLE modules (
	id TEXT PRIMARY KEY, version TEXT NOT NULL DEFAULT '', state TEXT NOT NULL DEFAULT 'disabled',
	error TEXT NOT NULL DEFAULT '', updated_at INTEGER NOT NULL
);`},
	// Windows profiles and local consent records (api-v1 §6B). consent_blob is
	// the DPAPI-bound ciphertext and never leaves the device; proof_digest is
	// its SHA-256 (hex) as reported to the server.
	// state: active_default | requested | authorised | blacklisted |
	// cleared_pending_consent | revoked.
	{Version: 6, Name: "profiles", SQL: `
CREATE TABLE profiles (
	sid TEXT PRIMARY KEY, username TEXT NOT NULL DEFAULT '', profile_path TEXT NOT NULL DEFAULT '',
	state TEXT NOT NULL, consent_id TEXT NOT NULL DEFAULT '', consent_at INTEGER, consent_blob BLOB,
	proof_digest TEXT NOT NULL DEFAULT '', request_id TEXT NOT NULL DEFAULT '', request_scope TEXT NOT NULL DEFAULT '',
	requested_by TEXT NOT NULL DEFAULT '', blacklisted_at INTEGER, blacklist_reason TEXT NOT NULL DEFAULT '',
	last_security_check INTEGER, last_security_signal TEXT NOT NULL DEFAULT '', last_security_evidence TEXT NOT NULL DEFAULT '',
	reported_at INTEGER, updated_at INTEGER NOT NULL
);`},
	// Three-way sync base: the last agreed state of each path per sync folder
	// (api-v1 §10). mtime is unix nanoseconds like files.mtime.
	{Version: 7, Name: "sync_base", SQL: `
CREATE TABLE sync_base (
	folder_id TEXT NOT NULL, rel_path TEXT NOT NULL, node_id TEXT NOT NULL DEFAULT '', revision INTEGER NOT NULL,
	sha256 TEXT NOT NULL, size INTEGER NOT NULL, mtime INTEGER NOT NULL, updated_at INTEGER NOT NULL,
	PRIMARY KEY (folder_id, rel_path)
);`},
}

// baseline adopts a database created before schema_version existed: when
// every legacy table is present it is recorded as version 1. A partial
// legacy schema returns 0 so the idempotent migration 1 fills the gaps.
func baseline(tx *sql.Tx) (int, error) {
	for _, t := range legacyTables {
		ok, err := dbutil.TableExists(tx, t)
		if err != nil || !ok {
			return 0, err
		}
	}
	return 1, nil
}
