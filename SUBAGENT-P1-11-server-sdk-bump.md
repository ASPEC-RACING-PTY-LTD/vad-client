# P1-11 Server go.mod SDK bump (INT)
Status: completed

## Goal
Make the server consume the SDK version that contains `repo`.

## Changes
None needed. `vad-server/go.mod` uses the temporary `replace github.com/ASPEC-RACING-PTY-LTD/vad-client/sdk => ../VaultDock/sdk`, and `D:/VaultDock/go.work` points at the same checkout, so the server already builds against the current SDK, including `sdk/repo`. INT runs `go mod tidy` in the final audit.

## Notes for dependent tasks / INT
- P1-12 and P1-14 may import `sdk/vdcrypt`, `sdk/repo`, `sdk/pack` and `sdk/snapshot` directly.
- After `sdk/v0.1.0` is tagged and pushed by the owner, replace the placeholder require and drop the replace.
