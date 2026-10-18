import { useEffect, useState, type CSSProperties } from 'react'
import { WindowIsMaximised, WindowMinimise, WindowToggleMaximise } from '../wailsjs/runtime/runtime'
import { CloseWindow } from '../wailsjs/go/main/App'
import logo from './logo.png'

// Wails drag regions: the bar moves the window (double-click maximises);
// the buttons opt out.
const drag = { '--wails-draggable': 'drag' } as CSSProperties
const noDrag = { '--wails-draggable': 'no-drag' } as CSSProperties

/** The app's own title bar for the frameless window (Linear-style: no system chrome). */
export function TitleBar() {
  const [max, setMax] = useState(false)
  useEffect(() => {
    const sync = () => void WindowIsMaximised().then(setMax).catch(() => {})
    sync()
    window.addEventListener('resize', sync)
    return () => window.removeEventListener('resize', sync)
  }, [])
  return (
    <div style={drag} onDoubleClick={() => WindowToggleMaximise()} className="flex h-9 shrink-0 items-center select-none">
      <div className="flex items-center gap-2 pl-3.5 text-[12px] font-medium text-slate-500">
        <img src={logo} alt="" className="size-4 object-contain" />
        VaultDock
      </div>
      <div style={noDrag} className="ml-auto flex h-full" onDoubleClick={(e) => e.stopPropagation()}>
        <WinButton label="Minimise" onClick={() => WindowMinimise()}>
          <path d="M1 5.5h9" />
        </WinButton>
        <WinButton label={max ? 'Restore' : 'Maximise'} onClick={() => WindowToggleMaximise()}>
          {max ? (
            <>
              <rect x="1" y="3" width="7" height="7" rx="1" />
              <path d="M3.5 3V1.8a.8.8 0 0 1 .8-.8h5a.8.8 0 0 1 .8.8v5a.8.8 0 0 1-.8.8H8" />
            </>
          ) : (
            <rect x="1" y="1" width="9" height="9" rx="1.2" />
          )}
        </WinButton>
        <WinButton label="Close" danger onClick={() => void CloseWindow()}>
          <path d="M1.5 1.5l8 8M9.5 1.5l-8 8" />
        </WinButton>
      </div>
    </div>
  )
}

function WinButton({ label, onClick, danger, children }: { label: string; onClick: () => void; danger?: boolean; children: React.ReactNode }) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={onClick}
      className={
        'grid h-full w-11 place-items-center text-slate-500 transition-colors ' +
        (danger ? 'hover:bg-[#e5484d] hover:text-white' : 'hover:bg-slate-900/[.06] hover:text-slate-900 dark:hover:bg-white/[.07] dark:hover:text-slate-100')
      }
    >
      <svg viewBox="0 0 11 11" className="size-[11px]" fill="none" stroke="currentColor" strokeWidth="1.1" strokeLinecap="round" aria-hidden>
        {children}
      </svg>
    </button>
  )
}
