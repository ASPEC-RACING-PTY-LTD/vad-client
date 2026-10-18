package agent

import "github.com/ASPEC-RACING-PTY-LTD/VaultDock/sdk/wire"

// --- restore (DSK-G, Phase 2) ---

// SnapshotView is one snapshot of this account's repository as the device
// reads it (newest first). A pipe client sees only snapshots of its own
// Windows profile.
type SnapshotView struct {
	OID        string   `json:"oid"`
	Time       int64    `json:"time"` // unix ms
	JobID      int64    `json:"job_id"`
	JobName    string   `json:"job_name"`
	DeviceID   int64    `json:"device_id"`
	DeviceName string   `json:"device_name"`
	Hostname   string   `json:"hostname"`
	ProfileSID string   `json:"profile_sid"`
	Username   string   `json:"username"`
	Files      int64    `json:"files"`
	Bytes      int64    `json:"bytes"`
	Roots      []string `json:"roots"` // token paths
}

// BrowseRequest lists one folder of a snapshot; Path "" lists its roots.
type BrowseRequest struct {
	SnapshotOID string `json:"snapshot_oid"`
	Path        string `json:"path"`
}

// SnapshotEntry is one entry of a snapshot folder.
type SnapshotEntry struct {
	Path     string `json:"path"` // token path
	Name     string `json:"name"`
	Type     string `json:"type"` // file, dir, link
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"` // unix ms
}

// RestoreRequest restores snapshot paths on this device into the caller's
// own profile (in-process callers: the snapshot's profile).
//   - Selection is the token paths (empty = the whole snapshot).
//   - TargetFolder, when set, restores into that folder instead of the
//     original locations: {DOCUMENTS}/Reports goes to
//     <TargetFolder>\Documents\Reports.
//   - Policy is a wire.RestoreConflict* value ("" = rename).
type RestoreRequest struct {
	SnapshotOID  string                  `json:"snapshot_oid"`
	Selection    []wire.RestoreSelection `json:"selection"`
	TargetFolder string                  `json:"target_folder,omitempty"`
	Policy       string                  `json:"policy,omitempty"`
}

// RestorePlanView previews a RestoreRequest: totals and where each
// selected root goes; Blocked explains a target refused by system-file
// protection.
type RestorePlanView struct {
	Files   int64           `json:"files"`
	Bytes   int64           `json:"bytes"`
	Targets []RestoreTarget `json:"targets"`
}

// RestoreTarget is the destination of one selected path.
type RestoreTarget struct {
	Path    string `json:"path"`
	Target  string `json:"target"`
	Blocked string `json:"blocked,omitempty"`
}

// LocalRestore is a restore running or finished on this device, started
// locally or by the server (ServerID > 0).
type LocalRestore struct {
	ID          int64              `json:"id"`
	ServerID    int64              `json:"server_id,omitempty"`
	SnapshotOID string             `json:"snapshot_oid"`
	ProfileSID  string             `json:"profile_sid"`
	Status      string             `json:"status"` // wire.RestoreStatus*
	FilesTotal  int64              `json:"files_total"`
	FilesDone   int64              `json:"files_done"`
	BytesTotal  int64              `json:"bytes_total"`
	BytesDone   int64              `json:"bytes_done"`
	Skipped     int64              `json:"skipped"`
	Failed      int64              `json:"failed"`
	Blocked     int64              `json:"blocked"`
	Error       string             `json:"error,omitempty"`
	StartedAt   int64              `json:"started_at"`
	FinishedAt  int64              `json:"finished_at,omitempty"`
	Problems    []wire.RestoreItem `json:"problems,omitempty"` // the latest failed or blocked items
}
