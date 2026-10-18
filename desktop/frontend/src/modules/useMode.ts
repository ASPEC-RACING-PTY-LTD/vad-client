import { useEffect, useState } from 'react'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import { GetModeState } from '../../wailsjs/go/main/App'
import type { main } from '../../wailsjs/go/models'

// useMode returns the install mode and modules (null while loading) and
// follows the agent's "modules" events.
export function useMode(): main.ModeView | null {
  const [mode, setMode] = useState<main.ModeView | null>(null)
  useEffect(() => {
    let live = true
    GetModeState()
      .then((m) => live && setMode(m))
      .catch(() => live && setMode({ source: 'unknown', install_mode: '', modules: [] } as unknown as main.ModeView))
    const off = EventsOn('modules', (m: main.ModeView) => setMode(m))
    return () => {
      live = false
      off()
    }
  }, [])
  return mode
}
