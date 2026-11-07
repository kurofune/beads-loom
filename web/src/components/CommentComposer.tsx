import { useState } from 'react'
import { useAddComment } from '@/api/mutations'
import { useToast } from './Toast'
import { isSubmitEnter } from '@/lib/enter'

/**
 * Comment composer.
 *
 * Grows with the text rather than sitting as a fixed box, because most
 * comments on a bead are one line ("reproduced, fixing") and a five-row
 * textarea for one line is just noise in a 560px sheet.
 *
 * No optimistic insert: bd assigns the id and author, and a half-real comment
 * in the history feed is more confusing than a 300ms wait.
 */
export function CommentComposer({ project, id }: { project: string; id: string }) {
  const [text, setText] = useState('')
  const add = useAddComment(project)
  const toast = useToast()

  const submit = () => {
    const body = text.trim()
    if (!body || add.isPending) return
    add.mutate(
      { id, text: body },
      {
        onSuccess: () => setText(''),
        onError: (err) => toast.error('Could not add the comment', err),
      },
    )
  }

  return (
    <div>
      <textarea
        className="field w-full resize-none py-1.5 leading-[1.45]"
        style={{ height: text.includes('\n') || text.length > 64 ? 76 : 30 }}
        value={text}
        placeholder="Comment…"
        aria-label="Add a comment"
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (isSubmitEnter(e) && (e.metaKey || e.ctrlKey)) {
            e.preventDefault()
            submit()
          }
        }}
      />
      {text.trim() && (
        <div className="mt-1.5 flex items-center gap-2">
          <button
            type="button"
            className="btn h-6 text-[11.5px]"
            disabled={add.isPending}
            style={{ background: 'var(--accent)', color: 'var(--accent-text)', boxShadow: 'none' }}
            onClick={submit}
          >
            {add.isPending ? 'Posting…' : 'Comment'}
          </button>
          <button
            type="button"
            className="btn btn-quiet h-6 text-[11.5px]"
            onClick={() => setText('')}
          >
            Cancel
          </button>
          <span className="ml-auto text-[10.5px] text-faint">
            <kbd className="kbd">⌘</kbd> <kbd className="kbd">↵</kbd>
          </span>
        </div>
      )}
    </div>
  )
}
