import type { main } from '../../wailsjs/go/models'

// Module ids (api-v1 §6A, sdk/modules).
export type ModuleId = 'core' | 'sync' | 'automation' | 'profiles' | 'vss' | 'discovery' | 'reinstall'

// Screen ids of the desktop navigation, and the sections of the Sync screen
// (folders, and the backup sections when this PC has the backup service).
export type ScreenId = 'overview' | 'sync' | 'transfers' | 'automations' | 'settings'
export type SyncSection = 'configs' | 'backups' | 'readiness' | 'issues'

// screenRequires: a screen is shown only when every listed module is
// enabled (MASTER_PLAN §7.7 "UI gating"). Hidden screens' data stays intact.
export const screenRequires: Record<ScreenId | SyncSection, ModuleId[]> = {
  overview: ['sync'],
  sync: [],
  configs: ['sync'],
  backups: ['core'],
  readiness: ['core'],
  issues: ['core'],
  transfers: ['sync'],
  automations: ['automation'],
  settings: [],
}

// enabledModules lists the enabled module ids, or null when the mode is
// unknown (then nothing is hidden; the agent and server still enforce).
export function enabledModules(mode: main.ModeView | null): Set<string> | null {
  if (!mode || mode.source === 'unknown' || !mode.modules) return null
  return new Set(mode.modules.filter((m) => m.state === 'enabled').map((m) => m.id))
}

export function screenVisible(id: ScreenId | SyncSection, enabled: Set<string> | null): boolean {
  if (!enabled) return true
  return screenRequires[id].every((m) => enabled.has(m))
}

export function modeLabel(mode: main.ModeView | null): string {
  switch (mode?.install_mode) {
    case 'simple':
      return 'Simple'
    case 'advanced':
      return 'Advanced'
    default:
      return 'Unknown mode'
  }
}
