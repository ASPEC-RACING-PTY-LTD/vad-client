import { useEffect, useState } from 'react'
import { ExternalLink, RefreshCw, Workflow } from 'lucide-react'
import { ListAutomations, OpenWebInterface, RefreshAutomations } from '../../wailsjs/go/main/App'
import type { engine } from '../../wailsjs/go/models'
import { Badge, Button } from '../ui'

/**
 * AutomationsCard lists the account's automations on the Overview. They are
 * built in the web interface and run here as files change; they can route,
 * rename, tag, skip and notify, but never modify files on this PC.
 */
export function AutomationsCard() {
  const [list, setList] = useState<engine.AutomationView[]>([])
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    ListAutomations()
      .then((l) => setList(l ?? []))
      .catch(() => {})
  }, [])
  return (
    <div className="card mt-4 overflow-hidden">
      <div className="flex items-center gap-2 border-b border-slate-200 px-5 py-2.5 text-sm dark:border-slate-800">
        <Workflow className="size-4 text-brand-500" />
        <span className="font-semibold">Automations</span>
        <span className="ml-auto" />
        <Button
          size="sm"
          variant="ghost"
          icon={<RefreshCw className="size-3.5" />}
          loading={busy}
          onClick={async () => {
            setBusy(true)
            setList((await RefreshAutomations().catch(() => list)) ?? [])
            setBusy(false)
          }}
        >
          Refresh
        </Button>
        <Button size="sm" variant="ghost" icon={<ExternalLink className="size-3.5" />} onClick={() => OpenWebInterface('/automations')}>
          Open builder
        </Button>
      </div>
      {list.length === 0 ? (
        <div className="px-5 py-3 text-sm text-slate-500">No automations yet. Build them in the web interface; they apply to sync folders set to use them.</div>
      ) : (
        <div className="max-h-[220px] divide-y divide-slate-100 overflow-y-auto dark:divide-slate-800">
          {list.map((a) => (
            <div key={a.id} className="flex flex-wrap items-center gap-3 px-5 py-2.5">
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-medium">{a.name}</div>
                <div className="mt-1 flex flex-wrap gap-1.5">
                  {a.triggers.map((t, i) => (
                    <Badge key={'t' + i} tone="violet">
                      {t}
                    </Badge>
                  ))}
                  <span className="text-xs text-slate-400">→</span>
                  {[...new Set(a.actions)].map((t) => (
                    <Badge key={t} tone="green">
                      {t}
                    </Badge>
                  ))}
                </div>
              </div>
              {a.priority !== 0 && <Badge tone="blue">priority {a.priority}</Badge>}
              <Button size="sm" variant="ghost" onClick={() => OpenWebInterface(`/automations/${a.id}`)}>
                Edit
              </Button>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
