/**
 * The single shortcut registry.
 *
 * Everything in here is (a) actually implemented and (b) rendered in the `?`
 * cheat sheet straight from this array, plus the shortcuts the mounted
 * integrations contribute — so the help can never drift from the bindings. ARCHITECTURE.md §Keyboard model lists the full target set; entries
 * land here as the views that own them ship, not before.
 *
 * Hard rule from the work package: every shortcut is also reachable by mouse.
 */
export type ShortcutGroup = 'Navigation' | 'Issue' | 'View' | 'Global'

/**
 * Which surfaces a binding is live on.
 *
 * The cheat sheet groups by this rather than by category alone, because
 * "does `z` do anything here?" is the question someone actually has, and a flat
 * list makes them guess. `'*'` means everywhere. Integration views add their
 * own sections through their `views` slot.
 */
export type Surface = '*' | 'initiatives' | 'backlog' | 'board' | 'graph' | 'now' | 'detail'

export const SURFACE_LABEL: Record<Surface, string> = {
  '*': 'Everywhere',
  initiatives: 'Initiatives',
  backlog: 'Backlog',
  board: 'Board',
  graph: 'Graph',
  now: 'Now',
  detail: 'Issue detail',
}

export interface Shortcut {
  id: string
  /** Literal keys, in press order. Two entries = a chord, e.g. ['g','l']. */
  keys: string[]
  /** Equivalent bindings — arrow keys for j/k, etc. Matched, not displayed. */
  aliases?: string[][]
  /**
   * Requires the platform modifier (Cmd on macOS, Ctrl elsewhere). Bindings
   * without this are ignored while a modifier is held, so ⌘S still saves the
   * page instead of opening the status menu.
   */
  mod?: boolean
  label: string
  group: ShortcutGroup
  /** How the binding is rendered in the cheat sheet, if not just the keys. */
  display?: string[]
  /** The mouse-reachable equivalent, shown alongside so nothing is keys-only. */
  alsoBy?: string
  /** Where the binding is live. Defaults to everywhere. */
  surfaces?: Surface[]
}

export const SHORTCUTS: Shortcut[] = [
  {
    id: 'row.next',
    surfaces: ['initiatives', 'backlog', 'board', 'now'],
    keys: ['j'],
    aliases: [['ArrowDown']],
    display: ['j', '↓'],
    label: 'Move down a row',
    group: 'Navigation',
    alsoBy: 'Click a row',
  },
  {
    id: 'row.prev',
    surfaces: ['initiatives', 'backlog', 'board', 'now'],
    keys: ['k'],
    aliases: [['ArrowUp']],
    display: ['k', '↑'],
    label: 'Move up a row',
    group: 'Navigation',
    alsoBy: 'Click a row',
  },
  {
    id: 'row.first',
    surfaces: ['initiatives', 'backlog', 'board'],
    keys: ['g', 'g'],
    label: 'Jump to the first row',
    group: 'Navigation',
  },
  {
    id: 'row.last',
    surfaces: ['initiatives', 'backlog', 'board'],
    keys: ['G'],
    label: 'Jump to the last row',
    group: 'Navigation',
  },
  {
    id: 'issue.open',
    surfaces: ['initiatives', 'backlog', 'board', 'graph', 'now'],
    keys: ['Enter'],
    label: 'Open the focused issue',
    group: 'Issue',
    alsoBy: 'Click a row',
  },
  {
    id: 'issue.close',
    surfaces: ['initiatives', 'backlog', 'board', 'graph'],
    keys: ['Escape'],
    label: 'Close the detail panel · clear search',
    group: 'Issue',
    alsoBy: 'Click the ✕, or the backdrop',
  },
  {
    id: 'card.prevColumn',
    surfaces: ['board'],
    keys: ['ArrowLeft'],
    display: ['←'],
    label: 'Move to the column on the left',
    group: 'Navigation',
    alsoBy: 'Click a card',
  },
  {
    id: 'card.nextColumn',
    surfaces: ['board'],
    keys: ['ArrowRight'],
    display: ['→'],
    label: 'Move to the column on the right',
    group: 'Navigation',
    alsoBy: 'Click a card',
  },
  {
    id: 'select.toggle',
    surfaces: ['backlog', 'board'],
    keys: ['x'],
    label: 'Select / deselect the focused issue',
    group: 'Issue',
    alsoBy: '⌘-click a card or the backlog checkbox, shift-click for a range',
  },
  {
    id: 'select.all',
    surfaces: ['backlog', 'board'],
    keys: ['A'],
    display: ['⇧', 'a'],
    label: 'Select every visible issue',
    group: 'Issue',
  },
  {
    id: 'issue.create',
    surfaces: ['backlog', 'board'],
    keys: ['c'],
    label: 'New issue',
    group: 'Issue',
    alsoBy: 'Click New in the top bar',
  },
  {
    id: 'edit.status',
    surfaces: ['backlog', 'board', 'detail'],
    keys: ['s'],
    label: 'Change status',
    group: 'Issue',
    alsoBy: 'Click the status chip, or drag the card on the board',
  },
  {
    id: 'edit.priority',
    surfaces: ['backlog', 'board', 'detail'],
    keys: ['p'],
    label: 'Change priority',
    group: 'Issue',
    alsoBy: 'Click the priority badge',
  },
  {
    id: 'edit.assignee',
    surfaces: ['backlog', 'board', 'detail'],
    keys: ['a'],
    label: 'Change assignee',
    group: 'Issue',
    alsoBy: 'Click the assignee monogram',
  },
  {
    id: 'edit.labels',
    surfaces: ['backlog', 'board', 'detail'],
    keys: ['l'],
    label: 'Edit labels',
    group: 'Issue',
    alsoBy: 'Click a label chip',
  },
  {
    id: 'group.toggle',
    surfaces: ['backlog'],
    keys: ['z'],
    label: "Collapse or expand the focused row's group",
    group: 'View',
    alsoBy: 'Click a group header',
  },
  {
    id: 'search.focus',
    surfaces: ['backlog', 'board'],
    keys: ['/'],
    label: 'Filter the backlog',
    group: 'View',
    alsoBy: 'Click the search field',
  },
  {
    id: 'view.initiatives',
    keys: ['g', 'i'],
    label: 'Go to initiatives',
    group: 'View',
    alsoBy: 'Click the Initiatives tab',
  },
  {
    id: 'view.board',
    keys: ['g', 'b'],
    label: 'Go to the board',
    group: 'View',
    alsoBy: 'Click Board in the top bar',
  },
  {
    id: 'view.graph',
    keys: ['g', 'd'],
    label: 'Go to the dependency graph',
    group: 'View',
    alsoBy: 'Click Graph in the top bar',
  },
  {
    id: 'view.now',
    keys: ['g', 'n'],
    label: 'Go to Now — agent activity across every project',
    group: 'View',
  },
  {
    id: 'view.backlog',
    keys: ['g', 'l'],
    label: 'Go to the backlog',
    group: 'View',
    alsoBy: 'Click Backlog in the top bar',
  },
  {
    id: 'theme.toggle',
    keys: ['t'],
    label: 'Switch theme',
    group: 'Global',
    alsoBy: 'Click the theme control in the sidebar footer',
  },
  {
    id: 'palette.open',
    keys: ['k'],
    mod: true,
    display: ['⌘', 'k'],
    label: 'Command palette — jump to a project or an issue',
    group: 'Global',
    alsoBy: 'Click a project in the sidebar',
  },
  {
    id: 'help.toggle',
    keys: ['?'],
    label: 'Keyboard shortcuts',
    group: 'Global',
    alsoBy: 'Click ? in the sidebar footer',
  },
]

export const GROUP_ORDER: ShortcutGroup[] = ['Navigation', 'Issue', 'View', 'Global']

export type ShortcutId = (typeof SHORTCUTS)[number]['id']
export type ShortcutHandlers = Partial<Record<string, (event: KeyboardEvent) => void>>
