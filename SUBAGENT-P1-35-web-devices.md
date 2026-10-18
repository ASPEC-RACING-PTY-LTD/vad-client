# P1-35 (WEB-7) Devices page: mode/modules badges, profiles panel, key status
Status: completed

## Goal
Extend the web Devices page (api-v1 §5A, §6A, §6B, §11B):
- install mode and module badges for each device;
- a per-device Windows profiles panel with state badges, consent requests (admin) and a superuser-only "clear blacklist" with typed confirmation, showing server errors verbatim;
- a server key status card.

The previous process exited mid-task. On resume, the only files present were `features/devices/types.ts` and `ModeBadges.tsx`, and they matched the intended design. The rest was finished from there.

## Changes (repo: vad-server, all under `web/src/`)
- `pages/Devices.tsx` (modified):
  - `Device` is now `DeviceModeFields & {…}`.
  - `<ServerKeyCard />` sits above the pending requests.
  - Each authorised device shows `<ModeBadges>`.
  - When the `profiles.v1` flag is present, each device gets a "Profiles" toggle button (with `aria-expanded`) that opens `<ProfilesPanel>` inline.
  - The page now uses `useUser`, `useFeatureFlags` and `useIsSuperuser`.
- `features/devices/types.ts` (created): `InstallMode`, `ModuleId`, `ModuleState`, `ModulesSummary {enabled, failed, updated_at}`, `DeviceModeFields`, `ModuleStatus`, `DeviceModuleState`, plus the `moduleLabel` and `modeLabel` helpers.
- `features/devices/ModeBadges.tsx` (created):
  - a mode badge;
  - green badges for enabled modules and red "X failed" badges for failed ones;
  - "Modules not reported" when `updated_at` is 0, so nothing is inferred from the mode;
  - nothing at all when the server omits both fields.
- `features/keys/types.ts` (created): `ServerIdentity`, `ServerIdentityResponse`, `ServerKeyStatus`, and `normalizeFingerprint` / `sameFingerprint` (§14 Rev 1.1b comparison rules).
- `features/keys/api.ts` (created): `keyKeys`, `fetchServerKeyStatus()` and `useServerKeyStatus()`. A `409 escrow_unavailable` becomes `{state:'unavailable', message}`; any other error is rethrown.
- `features/keys/ServerKeyCard.tsx` (created): shows the SIK fingerprint as the server sends it and the SEK version (`vN`). It shows an "Unavailable" badge plus an explanation for `escrow_unavailable`, and `code: message` for any other error.
- `features/profiles/types.ts` (created): `DeviceProfile`, `ProfileState`, `BlacklistReason`, `ProfileConsentRequest`, `ProfileClearRequest`, the state and reason labels, `canRequestConsent` and `clearConfirmText`.
- `features/profiles/api.ts` (created): `profileKeys`, `useDeviceProfiles`, `useRequestConsent`, `useClearBlacklist` and `useIsSuperuser`.
- `features/profiles/ProfilesPanel.tsx` (created): a table of profiles with state badges and details (blacklist reason and date, cleared date, consent date, request scope).
  - Admin actions:
    - a checkbox on each requestable profile;
    - "Request consent (N)" sends `{sids}`;
    - "Request consent for all profiles" sends `{all:true}`.
  - Superuser action: "Clear blacklist" opens the shared `confirm({requireText})` dialog and sends that text as `confirm_text`.
  - Server errors appear inline as `code: message`, for example `not_superuser: …`.
- Tests (created):
  - `features/devices/__tests__/devices.test.tsx`: 10 tests covering the badges and the page (keys available and unavailable, flag gating, states and requests, superuser clear, `not_superuser` shown verbatim, the non-superuser admin view, and the plain-user view).
  - `features/keys/__tests__/keys.test.ts`: 4 tests.
  - `features/profiles/__tests__/profiles.test.ts`: 4 tests.

No `module.tsx` was added. These features have no routes of their own; the Devices page is still the existing core page.

## Interfaces exposed
```ts
// features/devices
export function ModeBadges({ device }: { device: DeviceModeFields }): JSX.Element | null
export interface ModulesSummary { enabled: string[]; failed: string[]; updated_at: number }
export interface DeviceModeFields { install_mode?: InstallMode | string; modules_summary?: ModulesSummary }
// features/keys
export type ServerKeyStatus = { state: 'available'; identity: ServerIdentity } | { state: 'unavailable'; message: string }
export async function fetchServerKeyStatus(): Promise<ServerKeyStatus>
export function useServerKeyStatus()            // key ['keys','server']
export function ServerKeyCard(): JSX.Element
export function sameFingerprint(a: string, b: string): boolean
// features/profiles
export function useDeviceProfiles(deviceId: number, enabled?: boolean)   // key ['profiles','list',{deviceId}]
export function useRequestConsent()              // vars {deviceId, body: {sids}|{all:true}}
export function useClearBlacklist()              // vars {profileId, confirmText}; invalidates profiles + summary
export function useIsSuperuser(user: User): boolean | undefined
export function ProfilesPanel(props: { deviceId: number; deviceName: string; isAdmin: boolean; isSuperuser: boolean | undefined })
```

## Commands run and results
All were run from `D:/VaultDock/vad-server/web`:
- `npx vitest run src/features/devices src/features/profiles src/features/keys`: 3 files, 19/19 passed. The Devices page test lives in `features/devices/__tests__`.
- `npx vitest run` (full suite): 13 files, 67/67 passed.
- `npm run build`: ok.
- `npm run lint`: 0 errors and 102 warnings, the same count as the P1-32 baseline.
  - `npx eslint` on the owned files gives 1 warning, the existing `sync_state: any` in `Devices.tsx`.

## Contract issues
- `GET /auth/me` does not return `is_superuser`, and §11B gives the web app no way to discover it. As a workaround, `useIsSuperuser` reads `GET /admin/users` (admins only) and finds the caller's row. If `/auth/me` ever carries `is_superuser`, that value is used directly.
- `confirm_text` for clear has no value defined in the contract. P1-20 accepts the SID or the username. The UI asks for the username, or the SID when there is no username.

## Gaps / follow-ups
- `GET /devices/{id}/modules` (versions, per-module errors, `core_version`) is not shown. The badges use `modules_summary` from `GET /devices`.
- `POST /devices/{id}/mode-request` has no UI.
- SEK rotation (`POST /admin/keys/rotate-server`) has no UI.
- I did not check the page in a browser. The tests run in jsdom only.

## Notes for dependent tasks / INT
- Server: consider adding `is_superuser` to `userView` in `handleMe` (`internal/api/auth_handlers.go`). Admins could then skip the `/admin/users` lookup; `useIsSuperuser` already prefers that field.
- The profiles panel and its toggle only appear when `/info` advertises `profiles.v1`. The mode badges render whenever `GET /devices` includes the §6A fields.
