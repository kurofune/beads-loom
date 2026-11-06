import ReactMarkdown, { defaultUrlTransform } from 'react-markdown'
import remarkGfm from 'remark-gfm'
import {
  ISSUE_LINK_SCHEME,
  RUN_LINK_SCHEME,
  issueIdFromHref,
  runIdFromHref,
} from '@/lib/updates'

/**
 * All four bead prose fields are markdown (ARCHITECTURE.md §Bead field
 * conventions). Styling lives in `.prose-loom` in index.css so it inherits the
 * theme tokens; this component only supplies the plumbing.
 *
 * Note: acceptance_criteria deliberately does NOT go through here — its
 * convention is one criterion per line, and a markdown renderer would fuse
 * adjacent lines into one paragraph. See splitCriteria().
 */
export function Markdown({
  children,
  onIssueLink,
  onRunLink,
}: {
  children: string
  /**
   * Handles an in-app issue link (`loom-issue:<id>`), which is what the weekly
   * updates sheet writes for a citation whose bead exists. Absent means no
   * body contains one, and every link stays an external link.
   */
  onIssueLink?: (id: string) => void
  /** The same for a run link (`loom-run:<id>`) the sheet writes for a run Loom has. */
  onRunLink?: (id: string) => void
}) {
  return (
    <div className="prose-loom">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        // react-markdown blanks every protocol outside its safe list, which
        // would strip the in-app schemes before the link component ever sees
        // them. Each scheme is let through only when a handler will intercept
        // it: without one, a literal loom-issue: link written in bead prose
        // would render as a live link on a scheme no browser can open.
        // Everything else still goes through the default sanitizer.
        urlTransform={(url) => {
          if (onIssueLink && url.startsWith(ISSUE_LINK_SCHEME)) return url
          if (onRunLink && url.startsWith(RUN_LINK_SCHEME)) return url
          return defaultUrlTransform(url)
        }}
        components={{
          // Bead prose routinely references other beads and files; keep links
          // safe and out-of-flow rather than navigating the app away.
          a: ({ children: text, href }) => {
            // An in-app navigation, not a page load: the sheet swaps to the
            // target rather than opening a tab on a dead scheme.
            const issueID = onIssueLink ? issueIdFromHref(href) : null
            if (issueID) {
              return (
                <InAppLink href={href} label={issueID} onOpen={() => onIssueLink?.(issueID)}>
                  {text}
                </InAppLink>
              )
            }
            const runID = onRunLink ? runIdFromHref(href) : null
            if (runID) {
              return (
                <InAppLink href={href} label={`run ${runID}`} onOpen={() => onRunLink?.(runID)}>
                  {text}
                </InAppLink>
              )
            }
            return (
              <a href={href} target="_blank" rel="noreferrer noopener">
                {text}
              </a>
            )
          },
        }}
      >
        {children}
      </ReactMarkdown>
    </div>
  )
}

function InAppLink({
  href,
  label,
  onOpen,
  children,
}: {
  href: string | undefined
  label: string
  onOpen: () => void
  children: React.ReactNode
}) {
  return (
    <a
      href={href}
      className="mono"
      title={`Open ${label}`}
      onClick={(event) => {
        event.preventDefault()
        onOpen()
      }}
    >
      {children}
    </a>
  )
}

/** Empty prose fields collapse to a muted em dash, never a blank gap. */
export function ProseOrDash({ value }: { value: string | null | undefined }) {
  const text = value?.trim()
  if (!text) return <span className="text-faint">—</span>
  return <Markdown>{text}</Markdown>
}
