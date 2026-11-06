import { useEffect, useState, type ReactNode } from 'react'
import { useDiscovery } from '@/api/hooks'
import { CONFIG_PATH } from '@/lib/settings'
import { EmptyState } from './states'

export const PROJECT_SNIPPET = `projects:
  - name: myproject
    workdir: /path/to/your/project`

const FEEDBACK_MS = 2000

type CopyState = 'idle' | 'copied' | 'failed'

/**
 * What `/` shows when Loom has no project to open: where it looked, and the two
 * ways to give it one — create a workspace where discovery will find it, or
 * name an existing one in the config file.
 */
export function NoProjectsFound() {
  return (
    <EmptyState
      title="No projects found"
      detail={
        <>
          Loom shows every beads workspace — a folder with a <span className="mono">.beads/</span>{' '}
          directory — that it discovers or that <span className="mono">{CONFIG_PATH}</span> lists.
        </>
      }
      action={
        <div className="w-full max-w-[52ch] space-y-4 text-left">
          <SearchedRoots />
          <Step title="Create a workspace">
            <p className="text-[11.5px] leading-relaxed text-faint">
              In your project folder, run this, then start <span className="mono">loom</span> there:
            </p>
            <pre className="mono rounded-md bg-sunken px-3 py-2 text-[11px] text-muted ring-1 ring-line">
              bd init
            </pre>
          </Step>
          <Step title="Or name an existing one">
            <p className="text-[11.5px] leading-relaxed text-faint">
              Add this to <span className="mono">{CONFIG_PATH}</span>, set{' '}
              <span className="mono">workdir</span>, and restart Loom:
            </p>
            <Snippet text={PROJECT_SNIPPET} />
          </Step>
        </div>
      }
    />
  )
}

function Step({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="space-y-1.5">
      <p className="legend">{title}</p>
      {children}
    </section>
  )
}

function SearchedRoots() {
  const discovery = useDiscovery()
  const status = discovery.data

  let body: ReactNode
  if (discovery.isLoading) {
    body = <p className="text-[11.5px] text-faint">Reading the discovery roots…</p>
  } else if (discovery.error) {
    body = (
      <p className="text-[11.5px]" style={{ color: 'var(--danger)' }} role="alert">
        Could not read the discovery roots: {discovery.error.message}
      </p>
    )
  } else if (!status?.enabled) {
    body = (
      <p className="text-[11.5px] leading-relaxed text-faint">
        Discovery is off, so Loom searched nowhere. Turn it on in Settings → Project discovery.
      </p>
    )
  } else {
    body = (
      <>
        <p className="text-[11.5px] leading-relaxed text-faint">
          Loom searched these folders, {status.depth} {status.depth === 1 ? 'level' : 'levels'} deep:
        </p>
        <ul aria-label="Discovery roots" className="space-y-0.5">
          {[...new Set(status.roots)].map((root) => (
            <li key={root} className="mono break-all text-[11px] text-muted">
              {root}
            </li>
          ))}
        </ul>
      </>
    )
  }

  return <Step title="Where Loom looked">{body}</Step>
}

function Snippet({ text }: { text: string }) {
  const [state, setState] = useState<CopyState>('idle')

  useEffect(() => {
    if (state === 'idle') return
    const timer = window.setTimeout(() => setState('idle'), FEEDBACK_MS)
    return () => window.clearTimeout(timer)
  }, [state])

  const copy = () => {
    const clipboard = navigator.clipboard
    if (!clipboard) {
      setState('failed')
      return
    }
    clipboard.writeText(text).then(
      () => setState('copied'),
      () => setState('failed'),
    )
  }

  return (
    <div className="flex items-start gap-3 rounded-md bg-sunken px-3 py-2 ring-1 ring-line">
      <pre className="mono min-w-0 flex-1 overflow-x-auto text-[11px] leading-relaxed text-muted">
        {text}
      </pre>
      <button
        type="button"
        className="btn shrink-0"
        onClick={copy}
        title="Copy the projects snippet"
        aria-live="polite"
      >
        {state === 'copied' ? 'Copied' : state === 'failed' ? 'Copy failed' : 'Copy'}
      </button>
    </div>
  )
}
