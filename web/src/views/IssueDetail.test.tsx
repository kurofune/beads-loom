import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import type { IssueDetail as Detail } from '@/api/types'
import { ToastProvider } from '@/components/Toast'
import { IssueDetail } from './IssueDetail'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const mocked = vi.hoisted(() => ({
  detail: undefined as unknown,
  patchMutate: vi.fn(),
  locks: [] as boolean[],
}))

vi.mock('@/api/hooks', () => ({
  useIssue: () => ({ data: mocked.detail, isLoading: false, error: null, refetch: vi.fn() }),
  // No project carries an integration, so no contributed section mounts.
  useProjects: () => ({ data: [] }),
}))
vi.mock('@/api/mutations', () => ({
  useReopenIssue: () => ({ mutate: vi.fn(), isPending: false }),
  usePatchIssue: () => ({ mutate: mocked.patchMutate, isPending: false }),
}))
vi.mock('@/components/Live', () => ({
  useInteractionLock: (active: boolean) => mocked.locks.push(active),
}))
vi.mock('@/lib/recent', () => ({ rememberIssue: () => undefined }))
vi.mock('@/components/CommentComposer', () => ({ CommentComposer: () => null }))
vi.mock('@/components/CloseDialog', () => ({ CloseDialog: () => null }))
vi.mock('@/components/EditMenu', () => ({ EditMenu: () => null }))

const explanation = 'closed 2 in 14d, 9 remain, due in 10d — required rate 0.90/day; achieving 16% of it (at-risk)'

const epic: Detail = {
  id: 'epic-health',
  title: 'Health detail fixture',
  type: 'epic',
  status: 'open',
  priority: 2,
  assignee: '',
  labels: [],
  estimate: null,
  created: '2026-08-01T00:00:00Z',
  updated: '2026-08-03T00:00:00Z',
  closed: null,
  due: '2026-08-13T00:00:00Z',
  deferUntil: null,
  ready: true,
  missingAcceptance: false,
  missingCloseReason: false,
  pinned: false,
  notReady: null,
  depCounts: { blockedBy: 0, blocks: 0 },
  parent: null,
  childStats: { total: 2, closed: 1, started: 0 },
  health: { state: 'at-risk', explanation },
  description: '',
  acceptanceCriteria: '',
  design: '',
  notes: '',
  closeReason: '',
  owner: '',
  deps: { blocks: [], blockedBy: [], children: [], discoveredFrom: [], parent: null },
  comments: [],
  events: [],
}

let root: Root | undefined

afterEach(() => {
  if (root) act(() => root?.unmount())
  root = undefined
  document.body.replaceChildren()
  mocked.patchMutate.mockReset()
  mocked.locks.length = 0
})

it('shows epic health with the server explanation as visible detail text', () => {
  mocked.detail = epic
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)

  act(() => {
    root?.render(
      <ToastProvider>
        <IssueDetail
          project="loom"
          id={epic.id}
          onClose={vi.fn()}
          onNavigate={vi.fn()}
        />
      </ToastProvider>,
    )
  })

  const section = [...container.querySelectorAll('section')].find((candidate) =>
    candidate.querySelector('.legend')?.textContent === 'Health',
  )
  if (!(section instanceof HTMLElement)) throw new Error('Health section not found')
  const chip = section.querySelector('[aria-label^="at risk:"]')
  if (!(chip instanceof HTMLElement)) throw new Error('Health chip not found')
  expect(chip.textContent).toBe('at risk')
  expect(chip.getAttribute('title')).toBe(explanation)
  expect(chip.style.color).toBe('var(--warn)')
  expect(section.textContent).toContain(explanation)
})

it('shows an always-visible edit affordance for the title and every prose section', () => {
  mocked.detail = epic
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)

  act(() => {
    root?.render(
      <ToastProvider>
        <IssueDetail project="loom" id={epic.id} onClose={vi.fn()} onNavigate={vi.fn()} />
      </ToastProvider>,
    )
  })

  expect(
    [...container.querySelectorAll<HTMLButtonElement>('button[aria-label^="Edit "]')].map(
      (button) => button.getAttribute('aria-label'),
    ),
  ).toEqual(['Edit title', 'Edit description', 'Edit acceptance', 'Edit design', 'Edit notes'])
})

it('opens the description editor with the literal raw markdown source', () => {
  mocked.detail = { ...epic, description: 'Keep **bold** as source' }
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)

  act(() => {
    root?.render(
      <ToastProvider>
        <IssueDetail project="loom" id={epic.id} onClose={vi.fn()} onNavigate={vi.fn()} />
      </ToastProvider>,
    )
  })
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit description"]')?.click()
  })

  const editor = container.querySelector<HTMLTextAreaElement>('textarea[aria-label="Description"]')
  expect(editor?.value).toBe('Keep **bold** as source')
})

it('focuses a prose textarea on open with the caret at the end', () => {
  mocked.detail = { ...epic, description: 'Caret lands here' }
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)

  act(() => {
    root?.render(
      <ToastProvider>
        <IssueDetail project="loom" id={epic.id} onClose={vi.fn()} onNavigate={vi.fn()} />
      </ToastProvider>,
    )
  })
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit description"]')?.click()
  })

  const editor = container.querySelector<HTMLTextAreaElement>('textarea[aria-label="Description"]')
  expect(document.activeElement).toBe(editor)
  expect(editor?.selectionStart).toBe(editor?.value.length)
  expect(editor?.selectionEnd).toBe(editor?.value.length)
})

it('opens acceptance as one editor row per non-blank raw line without rewriting prefixes', () => {
  mocked.detail = { ...epic, acceptanceCriteria: '- keep prefix\n\n  keep raw spacing  \n   ' }
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)

  act(() => {
    root?.render(
      <ToastProvider>
        <IssueDetail project="loom" id={epic.id} onClose={vi.fn()} onNavigate={vi.fn()} />
      </ToastProvider>,
    )
  })
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit acceptance"]')?.click()
  })

  expect(
    [...container.querySelectorAll<HTMLInputElement>('input[aria-label^="Criterion "]')].map(
      (input) => input.value,
    ),
  ).toEqual(['- keep prefix', '  keep raw spacing  '])
})

it('focuses the first acceptance criterion when the editor opens', () => {
  mocked.detail = { ...epic, acceptanceCriteria: 'first criterion\nsecond criterion' }
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)

  act(() => {
    root?.render(
      <ToastProvider>
        <IssueDetail project="loom" id={epic.id} onClose={vi.fn()} onNavigate={vi.fn()} />
      </ToastProvider>,
    )
  })
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit acceptance"]')?.click()
  })

  const firstCriterion = container.querySelector<HTMLInputElement>('input[aria-label="Criterion 1"]')
  expect(document.activeElement).toBe(firstCriterion)
})

it('cancels only the editor when Escape is pressed immediately after opening', () => {
  mocked.detail = { ...epic, description: 'editing context' }
  const onClose = vi.fn()
  const outer = document.createElement('div')
  const container = document.createElement('div')
  outer.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') onClose()
  })
  outer.append(container)
  document.body.append(outer)
  root = createRoot(container)

  act(() => {
    root?.render(
      <ToastProvider>
        <IssueDetail project="loom" id={epic.id} onClose={onClose} onNavigate={vi.fn()} />
      </ToastProvider>,
    )
  })
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit description"]')?.click()
  })
  act(() => {
    document.activeElement?.dispatchEvent(
      new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }),
    )
  })

  expect(container.querySelector('textarea[aria-label="Description"]')).toBeNull()
  expect(onClose).not.toHaveBeenCalled()
  expect(container.querySelector('h2')?.textContent).toBe(epic.title)
})

it('resets an open editor without mutating when navigation loads a different issue', () => {
  const first = { ...epic, id: 'first-issue', title: 'First issue', description: 'first draft' }
  const second = { ...epic, id: 'second-issue', title: 'Second issue', description: 'second text' }
  mocked.detail = first
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)

  act(() => {
    root?.render(
      <ToastProvider>
        <IssueDetail project="loom" id={first.id} onClose={vi.fn()} onNavigate={vi.fn()} />
      </ToastProvider>,
    )
  })
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit description"]')?.click()
  })

  mocked.detail = second
  act(() => {
    root?.render(
      <ToastProvider>
        <IssueDetail project="loom" id={second.id} onClose={vi.fn()} onNavigate={vi.fn()} />
      </ToastProvider>,
    )
  })

  expect(container.querySelector('textarea[aria-label="Description"]')).toBeNull()
  expect(container.querySelector('h2')?.textContent).toBe(second.title)
  expect(mocked.patchMutate).not.toHaveBeenCalled()
})

it('saves design with an exact one-field patch and closes the editor immediately', () => {
  mocked.detail = { ...epic, design: 'old design' }
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)

  act(() => {
    root?.render(
      <ToastProvider>
        <IssueDetail project="loom" id={epic.id} onClose={vi.fn()} onNavigate={vi.fn()} />
      </ToastProvider>,
    )
  })
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit design"]')?.click()
  })
  const editor = container.querySelector<HTMLTextAreaElement>('textarea[aria-label="Design"]')
  if (!editor) throw new Error('Design editor not found')
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set?.call(
      editor,
      'new design',
    )
    editor.dispatchEvent(new Event('input', { bubbles: true }))
  })
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Save design"]')?.click()
  })

  expect(mocked.patchMutate).toHaveBeenCalledWith(
    { id: epic.id, patch: { design: 'new design' } },
    expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
  )
  expect(container.querySelector('textarea[aria-label="Design"]')).toBeNull()
})

it('saves acceptance as trimmed non-blank lines under the write-side key', () => {
  mocked.detail = { ...epic, acceptanceCriteria: 'first\nsecond\nthird' }
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)

  act(() => {
    root?.render(
      <ToastProvider>
        <IssueDetail project="loom" id={epic.id} onClose={vi.fn()} onNavigate={vi.fn()} />
      </ToastProvider>,
    )
  })
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit acceptance"]')?.click()
  })
  const rows = [...container.querySelectorAll<HTMLInputElement>('input[aria-label^="Criterion "]')]
  for (const [row, value] of rows.map((row, index) => [row, [' first ', '   ', ' third  '][index]!] as const)) {
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(row, value)
      row.dispatchEvent(new Event('input', { bubbles: true }))
    })
  }
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Save acceptance"]')?.click()
  })

  expect(mocked.patchMutate).toHaveBeenCalledWith(
    { id: epic.id, patch: { acceptance: 'first\nthird' } },
    expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
  )
})

it('requires confirmation before clearing acceptance and sends an exact empty patch on Clear', () => {
  mocked.detail = { ...epic, acceptanceCriteria: 'first criterion\nsecond criterion' }
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)

  act(() => {
    root?.render(
      <ToastProvider>
        <IssueDetail project="loom" id={epic.id} onClose={vi.fn()} onNavigate={vi.fn()} />
      </ToastProvider>,
    )
  })
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit acceptance"]')?.click()
  })
  const rows = [...container.querySelectorAll<HTMLInputElement>('input[aria-label^="Criterion "]')]
  for (const row of rows) {
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(row, '')
      row.dispatchEvent(new Event('input', { bubbles: true }))
    })
  }
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Save acceptance"]')?.click()
  })

  expect(container.textContent).toContain('This clears acceptance.')
  expect(mocked.patchMutate).not.toHaveBeenCalled()

  act(() => {
    [...container.querySelectorAll('button')].find((button) => button.textContent === 'Clear')?.click()
  })
  expect(mocked.patchMutate).toHaveBeenCalledWith(
    { id: epic.id, patch: { acceptance: '' } },
    expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
  )
})

it('restores the exact raw acceptance rows after a failed save', () => {
  mocked.detail = { ...epic, acceptanceCriteria: '- keep raw \nsecond' }
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)

  act(() => {
    root?.render(
      <ToastProvider>
        <IssueDetail project="loom" id={epic.id} onClose={vi.fn()} onNavigate={vi.fn()} />
      </ToastProvider>,
    )
  })
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit acceptance"]')?.click()
  })
  act(() => {
    container
      .querySelector<HTMLInputElement>('input[aria-label="Criterion 1"]')
      ?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
  })
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Save acceptance"]')?.click()
  })

  const callbacks = mocked.patchMutate.mock.calls[0]?.[1] as
    | { onError?: (error: Error) => void }
    | undefined
  act(() => callbacks?.onError?.(new Error('write failed')))

  expect(
    [...container.querySelectorAll<HTMLInputElement>('input[aria-label^="Criterion "]')].map(
      (input) => input.value,
    ),
  ).toEqual(['- keep raw ', '', 'second'])
})

it('requires inline confirmation before clearing and keep editing performs no write', () => {
  mocked.detail = { ...epic, description: 'important text' }
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)

  act(() => {
    root?.render(
      <ToastProvider>
        <IssueDetail project="loom" id={epic.id} onClose={vi.fn()} onNavigate={vi.fn()} />
      </ToastProvider>,
    )
  })
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit description"]')?.click()
  })
  const editor = container.querySelector<HTMLTextAreaElement>('textarea[aria-label="Description"]')
  if (!editor) throw new Error('Description editor not found')
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set?.call(editor, '')
    editor.dispatchEvent(new Event('input', { bubbles: true }))
  })
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Save description"]')?.click()
  })

  expect(container.textContent).toContain('This clears description.')
  expect(mocked.patchMutate).not.toHaveBeenCalled()

  act(() => {
    [...container.querySelectorAll('button')].find((button) => button.textContent === 'Keep editing')?.click()
  })
  expect(mocked.patchMutate).not.toHaveBeenCalled()
  expect(container.querySelector('textarea[aria-label="Description"]')).not.toBeNull()
})

it('requires clear confirmation for a whitespace-only prose draft and sends no patch', () => {
  mocked.detail = { ...epic, description: 'important text' }
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)

  act(() => {
    root?.render(
      <ToastProvider>
        <IssueDetail project="loom" id={epic.id} onClose={vi.fn()} onNavigate={vi.fn()} />
      </ToastProvider>,
    )
  })
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit description"]')?.click()
  })
  const editor = container.querySelector<HTMLTextAreaElement>('textarea[aria-label="Description"]')
  if (!editor) throw new Error('Description editor not found')
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set?.call(editor, '   ')
    editor.dispatchEvent(new Event('input', { bubbles: true }))
  })
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Save description"]')?.click()
  })

  expect(container.textContent).toContain('This clears description.')
  expect(mocked.patchMutate).not.toHaveBeenCalled()
  expect(editor.value).toBe('   ')
})

it('sends an empty one-field patch only after the explicit clear click', () => {
  mocked.detail = { ...epic, notes: 'remove me' }
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)

  act(() => {
    root?.render(
      <ToastProvider>
        <IssueDetail project="loom" id={epic.id} onClose={vi.fn()} onNavigate={vi.fn()} />
      </ToastProvider>,
    )
  })
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit notes"]')?.click()
  })
  const editor = container.querySelector<HTMLTextAreaElement>('textarea[aria-label="Notes"]')
  if (!editor) throw new Error('Notes editor not found')
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set?.call(editor, '')
    editor.dispatchEvent(new Event('input', { bubbles: true }))
  })
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Save notes"]')?.click()
  })
  act(() => {
    [...container.querySelectorAll('button')].find((button) => button.textContent === 'Clear')?.click()
  })

  expect(mocked.patchMutate).toHaveBeenCalledWith(
    { id: epic.id, patch: { notes: '' } },
    expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
  )
  expect(container.querySelector('textarea[aria-label="Notes"]')).toBeNull()
})

it('holds the interaction lock while editing and releases it after cancel and save', () => {
  mocked.detail = { ...epic, design: 'draft' }
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)

  act(() => {
    root?.render(
      <ToastProvider>
        <IssueDetail project="loom" id={epic.id} onClose={vi.fn()} onNavigate={vi.fn()} />
      </ToastProvider>,
    )
  })
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit design"]')?.click()
  })
  expect(mocked.locks.at(-1)).toBe(true)

  act(() => {
    [...container.querySelectorAll('button')].find((button) => button.textContent === 'Cancel')?.click()
  })
  expect(mocked.locks.at(-1)).toBe(false)

  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit design"]')?.click()
  })
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Save design"]')?.click()
  })
  expect(mocked.locks.at(-1)).toBe(false)
})

it('reopens the failed draft and shows an error toast when the patch fails', () => {
  mocked.detail = { ...epic, notes: 'old notes' }
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)

  act(() => {
    root?.render(
      <ToastProvider>
        <IssueDetail project="loom" id={epic.id} onClose={vi.fn()} onNavigate={vi.fn()} />
      </ToastProvider>,
    )
  })
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit notes"]')?.click()
  })
  const editor = container.querySelector<HTMLTextAreaElement>('textarea[aria-label="Notes"]')
  if (!editor) throw new Error('Notes editor not found')
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set?.call(
      editor,
      'unsaved draft',
    )
    editor.dispatchEvent(new Event('input', { bubbles: true }))
  })
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Save notes"]')?.click()
  })

  const callbacks = mocked.patchMutate.mock.calls[0]?.[1] as
    | { onError?: (error: Error) => void }
    | undefined
  act(() => callbacks?.onError?.(new Error('write failed')))

  expect(container.querySelector<HTMLTextAreaElement>('textarea[aria-label="Notes"]')?.value).toBe(
    'unsaved draft',
  )
  expect(document.body.textContent).toContain(`Could not update notes of ${epic.id}`)
})

it('cancels locally on Escape and saves on Ctrl+Enter', () => {
  mocked.detail = { ...epic, design: 'keyboard draft' }
  const outer = document.createElement('div')
  const container = document.createElement('div')
  const escaped = vi.fn()
  outer.addEventListener('keydown', escaped)
  outer.append(container)
  document.body.append(outer)
  root = createRoot(container)

  act(() => {
    root?.render(
      <ToastProvider>
        <IssueDetail project="loom" id={epic.id} onClose={vi.fn()} onNavigate={vi.fn()} />
      </ToastProvider>,
    )
  })
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit design"]')?.click()
  })
  act(() => {
    container
      .querySelector<HTMLTextAreaElement>('textarea[aria-label="Design"]')
      ?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
  })
  expect(container.querySelector('textarea[aria-label="Design"]')).toBeNull()
  expect(escaped).not.toHaveBeenCalled()

  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit design"]')?.click()
  })
  act(() => {
    container
      .querySelector<HTMLTextAreaElement>('textarea[aria-label="Design"]')
      ?.dispatchEvent(
        new KeyboardEvent('keydown', { key: 'Enter', ctrlKey: true, bubbles: true }),
      )
  })

  expect(mocked.patchMutate).toHaveBeenCalledWith(
    { id: epic.id, patch: { design: 'keyboard draft' } },
    expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
  )
})

it('saves an exact-field patch on Cmd+Enter', () => {
  mocked.detail = { ...epic, notes: 'command-key draft' }
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)

  act(() => {
    root?.render(
      <ToastProvider>
        <IssueDetail project="loom" id={epic.id} onClose={vi.fn()} onNavigate={vi.fn()} />
      </ToastProvider>,
    )
  })
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit notes"]')?.click()
  })
  act(() => {
    container
      .querySelector<HTMLTextAreaElement>('textarea[aria-label="Notes"]')
      ?.dispatchEvent(
        new KeyboardEvent('keydown', { key: 'Enter', metaKey: true, bubbles: true }),
      )
  })

  expect(mocked.patchMutate).toHaveBeenCalledWith(
    { id: epic.id, patch: { notes: 'command-key draft' } },
    expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
  )
})

function renderDetail(detail: Detail) {
  mocked.detail = detail
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
  act(() => {
    root?.render(
      <ToastProvider>
        <IssueDetail project="loom" id={detail.id} onClose={vi.fn()} onNavigate={vi.fn()} />
      </ToastProvider>,
    )
  })
  return container
}

function typeTitle(container: HTMLElement, value: string) {
  const input = container.querySelector<HTMLInputElement>('input[aria-label="Title"]')
  if (!input) throw new Error('Title editor not found')
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  return input
}

it('edits the title in place and saves an exact one-field patch on Enter', () => {
  const container = renderDetail(epic)
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit title"]')?.click()
  })
  const input = typeTitle(container, '  Renamed from the sheet  ')
  expect(document.activeElement).toBe(input)
  act(() => {
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
  })

  expect(mocked.patchMutate).toHaveBeenCalledWith(
    { id: epic.id, patch: { title: 'Renamed from the sheet' } },
    expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
  )
  expect(container.querySelector('input[aria-label="Title"]')).toBeNull()
})

it('saves the title from the Save button and cancels on Escape without closing the sheet', () => {
  const outer = document.createElement('div')
  const escaped = vi.fn()
  outer.addEventListener('keydown', escaped)
  document.body.append(outer)
  const container = renderDetail(epic)
  outer.append(container)

  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit title"]')?.click()
  })
  const input = typeTitle(container, 'discarded')
  act(() => {
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
  })
  expect(container.querySelector('input[aria-label="Title"]')).toBeNull()
  expect(escaped).not.toHaveBeenCalled()
  expect(mocked.patchMutate).not.toHaveBeenCalled()
  expect(container.querySelector('h2')?.textContent).toBe(epic.title)

  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit title"]')?.click()
  })
  expect(container.querySelector<HTMLInputElement>('input[aria-label="Title"]')?.value).toBe(epic.title)
  typeTitle(container, 'Saved by button')
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Save title"]')?.click()
  })
  expect(mocked.patchMutate).toHaveBeenCalledWith(
    { id: epic.id, patch: { title: 'Saved by button' } },
    expect.anything(),
  )
})

it('sends no patch for a blank or unchanged title', () => {
  const container = renderDetail(epic)
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit title"]')?.click()
  })
  const blank = typeTitle(container, '   ')
  act(() => {
    blank.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
  })
  expect(mocked.patchMutate).not.toHaveBeenCalled()
  expect(container.querySelector('input[aria-label="Title"]')).not.toBeNull()
  expect(container.textContent).toContain('A title cannot be blank')

  const same = typeTitle(container, epic.title)
  act(() => {
    same.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
  })
  expect(mocked.patchMutate).not.toHaveBeenCalled()
  expect(container.querySelector('input[aria-label="Title"]')).toBeNull()
})

it('reopens the failed title draft with an error toast when the patch fails', () => {
  const container = renderDetail(epic)
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit title"]')?.click()
  })
  typeTitle(container, 'unsaved title')
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Save title"]')?.click()
  })
  const callbacks = mocked.patchMutate.mock.calls[0]?.[1] as
    | { onError?: (error: Error) => void }
    | undefined
  act(() => callbacks?.onError?.(new Error('write failed')))

  expect(container.querySelector<HTMLInputElement>('input[aria-label="Title"]')?.value).toBe(
    'unsaved title',
  )
  expect(document.body.textContent).toContain(`Could not update title of ${epic.id}`)
})

// jsdom never turns Enter on a focused button into a click; a browser does
// unless a keydown handler prevents the default.
function pressEnterOn(button: HTMLButtonElement) {
  act(() => {
    button.focus()
    const event = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })
    if (button.dispatchEvent(event)) button.click()
  })
}

it('discards the title draft on Enter at a focused Cancel and saves once on Enter at Save', () => {
  const container = renderDetail(epic)
  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit title"]')?.click()
  })
  typeTitle(container, 'draft to discard')
  const cancel = Array.from(container.querySelectorAll('button')).find(
    (button) => button.textContent === 'Cancel',
  )
  if (!cancel) throw new Error('Cancel button not found')
  pressEnterOn(cancel)

  expect(mocked.patchMutate).not.toHaveBeenCalled()
  expect(container.querySelector('input[aria-label="Title"]')).toBeNull()
  expect(container.querySelector('h2')?.textContent).toBe(epic.title)

  act(() => {
    container.querySelector<HTMLButtonElement>('button[aria-label="Edit title"]')?.click()
  })
  typeTitle(container, 'Saved by Enter on Save')
  const save = container.querySelector<HTMLButtonElement>('button[aria-label="Save title"]')
  if (!save) throw new Error('Save button not found')
  pressEnterOn(save)

  expect(mocked.patchMutate).toHaveBeenCalledTimes(1)
  expect(mocked.patchMutate).toHaveBeenCalledWith(
    { id: epic.id, patch: { title: 'Saved by Enter on Save' } },
    expect.anything(),
  )
})
