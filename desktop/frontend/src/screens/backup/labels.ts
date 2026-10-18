export type Tone = 'green' | 'blue' | 'amber' | 'red' | 'slate' | 'violet'

export const runStatus: Record<string, { label: string; tone: Tone }> = {
  queued: { label: 'Waiting', tone: 'slate' },
  scanning: { label: 'Scanning', tone: 'blue' },
  uploading: { label: 'Backing up', tone: 'blue' },
  committing: { label: 'Finishing', tone: 'blue' },
  completed: { label: 'Completed', tone: 'green' },
  partial: { label: 'Completed with skipped files', tone: 'amber' },
  failed: { label: 'Failed', tone: 'red' },
  cancelled: { label: 'Cancelled', tone: 'slate' },
  skipped: { label: 'Waiting for sign-in', tone: 'amber' },
}

export function runLabel(status: string | undefined): { label: string; tone: Tone } {
  if (!status) return { label: 'Never run', tone: 'slate' }
  return runStatus[status] ?? { label: status, tone: 'slate' }
}

export const runTrigger: Record<string, string> = {
  schedule: 'Scheduled',
  manual: 'Started by hand',
  automation: 'Automation',
  retry_skipped: 'Retry of skipped files',
  task: 'Requested from the web',
}

// Issue classes (api-v1 §6 + agent-local ones), in plain language.
export const issueClass: Record<string, string> = {
  locked: 'File was in use',
  access_denied: 'No permission to read',
  not_found: 'File disappeared during backup',
  path_too_long: 'Path too long',
  io: 'Disk read error',
  network: 'Network problem',
  server: 'Server problem',
  crypto: 'Encryption problem',
  cloud_placeholder: 'Cloud file not downloaded',
  vss: 'Snapshot problem',
  other: 'Other problem',
  profile_unauthorised: 'Profile not authorised',
  module_unavailable: 'Needs Advanced mode',
  guard_blocked: 'Protected system file',
  efs_needs_logon: 'Encrypted file: sign in to back it up',
  profile_needs_logon: 'Sign in to back up these files',
}

export const jobState: Record<string, { label: string; tone: Tone }> = {
  active: { label: 'Active', tone: 'green' },
  suspended_by_mode: { label: 'Paused: needs Advanced mode', tone: 'amber' },
  suspended_by_profile: { label: 'Paused: profile not authorised', tone: 'amber' },
}
