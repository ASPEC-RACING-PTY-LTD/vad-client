# P1-08 SDK-SCHED sdk/schedule
Status: completed

## Goal
A shared `sdk/schedule` package for the server scheduler and the desktop scheduler. It provides the schedule type (JSON matching the contract), validation, DST-correct `Next`, missed-run helpers (`Due`, at most one catch-up run) and deterministic per-device jitter. Stdlib only, plus the in-module `sdk/wire`.

## Changes (repo: path, created/modified/deleted, why)
All in the VaultDock client repo, all created:
- `sdk/schedule/schedule.go`: package doc with the DST rules, the `Schedule` type, constants, `Validate` and `ResolveLocation`.
- `sdk/schedule/cron.go`: a 5-field cron parser and day matcher.
- `sdk/schedule/next.go`: `Next`, the local-day scan and wall-time resolution (skipped and ambiguous times).
- `sdk/schedule/due.go`: `Options`, `JitterSeed`, `Jitter`, `NextRun`, `Decision` and `Due`.
- `sdk/schedule/schedule_test.go`: table tests. 63 subtests: 34 `Next` cases, 20 validation cases, a JSON/wire round trip, jitter and `Due`. The test binary embeds `time/tzdata`, and a test skips if a zone fails to load.

## Interfaces exposed
```go
type Schedule wire.JobSchedule // same fields and JSON as BackupJob.schedule; convert with Schedule(js) / wire.JobSchedule(s)
const TypeCron, TypeDaily, TypeWeekly, TypeInterval, TypeManual   // = wire.JobSchedule* constants
const CatchUpRunOnce, CatchUpSkip                                  // "" = run_once
const TimezoneLocal = "Local"
const MinEveryMinutes = 5; MaxEveryMinutes = 44640; MaxJitterMinutes = 1440
const DefaultGrace = 5 * time.Minute

func (s Schedule) Validate() error
func ResolveLocation(tz string, local *time.Location) (*time.Location, error) // ""/"Local" -> local (nil -> time.Local)
func (s Schedule) Next(after time.Time, loc *time.Location) time.Time  // no jitter; zero for manual/invalid/never
func JitterSeed(device string, jobID int64) uint64                    // FNV-1a + splitmix64, deterministic
func (s Schedule) Jitter(seed uint64) time.Duration                   // whole seconds in [0, jitter_minutes)
type Options struct { Loc *time.Location; JitterSeed uint64; Grace time.Duration }
func (s Schedule) NextRun(after time.Time, opt Options) time.Time     // Next + jitter, strictly after `after`
type Decision struct { Due, Missed bool; Next time.Time }
func (s Schedule) Due(last, now time.Time, opt Options) Decision
```
How times are computed:
- **DST:** a wall time skipped by DST runs at the end of the gap (Sydney 02:30 on spring-forward day → 03:00 AEDT). An ambiguous wall time runs once, at its first occurrence; the repeated hour is not run again. Several cron minutes inside one gap collapse into one run.
- **Interval:** runs on a grid of `every_minutes` counted from the Unix epoch. DST does not affect it.
- **Weekdays:** ISO numbering, 1 = Monday … 7 = Sunday.
- **Cron:** standard Vixie behaviour: `*`, `a-b`, `/n`, lists, month names jan..dec and day names sun..sat, with day-of-week 0 and 7 both Sunday. When both day-of-month and day-of-week are restricted, a day matches if either one matches.
- **Search limit:** `Next` searches up to about 8 years ahead (enough to reach Feb 29 across 2100), then returns the zero time.
- **`Due`:**
  - `last` is the start of the last run. For a job that never ran, pass the job's creation time; a zero `last` counts as overdue.
  - A slot in `(now-grace, now]` is on time and always runs.
  - Older missed slots produce one catch-up run under `run_once` (the default) or none under `skip`.
  - Record the run's start time as the next `last`; that prevents double runs.

## Commands run and results
- `cd sdk && gofmt -l schedule`: clean (after `gofmt -w` fixed CRLF line endings in `due.go`).
- `go vet ./schedule/`: clean.
- `go test -count=1 ./schedule/`: ok. The tests cover:
  - DST in Australia/Sydney (2026-10-04 gap, 2026-04-05 overlap) and America/New_York (2026-03-08 gap, 2026-11-01 overlap);
  - leap days (2028, and 2100 not being a leap year);
  - week boundaries and the year boundary.
- `go build ./...` (sdk): ok.

## Contract issues
- **Weekday numbering is unclear.** Rev 1.0 §6 (inherited by rev 1.1 §6) shows `"weekdays":[1,2,3,4,5]`, and wire says "Weekdays are 1..7", but neither says which day is 1. I used ISO (1 = Monday, 7 = Sunday), which makes the example mean Monday to Friday. INT should confirm this in an amendment.
- **Undefined semantics.** The contract does not define:
  - the `interval` anchor (I used an epoch-aligned grid);
  - the cron dialect (I used 5-field Vixie);
  - the grace period for "missed" (I used 5 minutes, configurable through `Options.Grace`);
  - how `jitter_minutes` is applied (I used a deterministic per device and job offset, applied as a delay).

## Gaps / follow-ups
- No `@daily`-style cron macros and no seconds field.
- `Validate` loads IANA zones, so it needs tzdata. Server Linux hosts have it. Windows desktop binaries should `import _ "time/tzdata"` in `main` (desktop owner).
- The schedules on `ReplicationPolicy` and `SyncFolder` are `json.RawMessage` in wire. Whether they use this shape is not defined in the contract; this package only targets BackupJob.schedule.

## Notes for dependent tasks / INT
- **The web `Schedule` type must change** (`vad-server/web/src/components/ScheduleEditor.tsx`, P0-20 owner) to the contract shape, and the weekday mapping must change with it:
  - **Shape:** `{type:'interval'|'daily'|'weekly'|'cron'|'manual', expr, at:'HH:MM', weekdays:number[], every_minutes, timezone, catch_up:'run_once'|'skip', jitter_minutes}`. This replaces the discriminant `kind` and the field `days`.
  - **Weekdays:** ISO 1 = Monday … 7 = Sunday. The editor currently uses 0 = Sunday; map with `iso = d === 0 ? 7 : d`.
  - **Interval range:** keep the minimum at 5 minutes, matching `MinEveryMinutes`.
- Server and desktop schedulers:
  - **Zone:** resolve it with `ResolveLocation(s.Timezone, deviceLoc)`. On the server, "Local" means the device's zone, so the server needs the device's IANA zone to compute `nextRuns` for the UI.
  - **Jitter seed:** use `JitterSeed(<stable device id/uuid>, jobID)`. Both sides must pass the same device string to get identical jitter.
  - **Loop:** call `Due(lastRunStart, now, opt)` each tick and start a run with trigger `schedule` when `Due` is true.
