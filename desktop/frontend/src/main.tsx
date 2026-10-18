import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import { App } from './App'
import { UIProvider } from './ui'
import { TitleBar } from './TitleBar'

const applyTheme = () => {
  const dark = window.matchMedia('(prefers-color-scheme: dark)').matches
  document.documentElement.classList.toggle('dark', dark)
}
applyTheme()
window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', applyTheme)

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <UIProvider>
      <div className="flex h-screen flex-col overflow-hidden">
        <TitleBar />
        <div className="relative min-h-0 flex-1 overflow-y-auto">
          <App />
        </div>
      </div>
    </UIProvider>
  </StrictMode>,
)
