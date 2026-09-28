import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { Badge, EmptyState, ErrorNote, Spinner } from './feedback'

describe('Spinner', () => {
  /**
   * A bare spinning div is invisible to a screen reader, so a loading screen
   * reads as an empty page. `role="status"` with a polite live region is the
   * minimum that announces "something is happening".
   */
  it('announces itself as a status region', () => {
    render(<Spinner />)
    expect(screen.getByRole('status')).toBeDefined()
  })

  it('announces politely rather than interrupting', () => {
    render(<Spinner />)
    expect(screen.getByRole('status').getAttribute('aria-live')).toBe('polite')
  })

  it('accepts a className', () => {
    render(<Spinner className="size-8" />)
    expect(screen.getByRole('status').className).toContain('size-8')
  })
})

describe('EmptyState', () => {
  it('renders the title', () => {
    render(<EmptyState title="No chats yet" />)
    expect(screen.getByText('No chats yet')).toBeDefined()
  })

  it('renders an optional description', () => {
    render(<EmptyState title="No chats yet" description="Start one with @username." />)
    expect(screen.getByText('Start one with @username.')).toBeDefined()
  })

  it('renders with only a title', () => {
    // Most empty states are one line; the rest of the props must be genuinely
    // optional, not merely typed as such.
    expect(() => render(<EmptyState title="Nothing here" />)).not.toThrow()
  })

  it('renders an action', () => {
    render(<EmptyState title="No chats yet" action={<button>New chat</button>} />)
    expect(screen.getByRole('button', { name: 'New chat' })).toBeDefined()
  })

  it('renders an icon', () => {
    render(<EmptyState title="No chats yet" icon={<span data-testid="icon" />} />)
    expect(screen.getByTestId('icon')).toBeDefined()
  })

  it('omits the description element entirely when absent', () => {
    // An empty <p> still occupies vertical space and pushes the layout around.
    const { container } = render(<EmptyState title="No chats yet" />)
    expect(container.querySelectorAll('p')).toHaveLength(1)
  })
})

describe('Badge', () => {
  it('renders its content', () => {
    render(<Badge>3</Badge>)
    expect(screen.getByText('3')).toBeDefined()
  })

  it('renders a large unread count', () => {
    render(<Badge>99+</Badge>)
    expect(screen.getByText('99+')).toBeDefined()
  })

  it('renders each tone with distinct classes', () => {
    const classesFor = (tone: 'accent' | 'muted' | 'danger') => {
      const { container, unmount } = render(<Badge tone={tone}>1</Badge>)
      const className = container.querySelector('span')!.className
      unmount()
      return className
    }

    const all = (['accent', 'muted', 'danger'] as const).map(classesFor)
    expect(new Set(all).size).toBe(all.length)
  })

  it('uses tabular numerals so a changing count does not jitter', () => {
    // An unread badge re-renders on every message; proportional digits would
    // make the whole chat row shift as the number changes.
    const { container } = render(<Badge>1</Badge>)
    expect(container.querySelector('span')!.className).toContain('tabular-nums')
  })

  it('reserves a minimum width so a single digit stays circular', () => {
    const { container } = render(<Badge>1</Badge>)
    expect(container.querySelector('span')!.className).toContain('min-w-5')
  })
})

describe('ErrorNote', () => {
  /**
   * `role="alert"` is an assertive live region: a failed send or a rejected
   * login has to interrupt, because the user is about to act on the assumption
   * that it worked.
   */
  it('announces itself as an alert', () => {
    render(<ErrorNote>Could not send</ErrorNote>)
    expect(screen.getByRole('alert')).toBeDefined()
  })

  it('renders its message', () => {
    render(<ErrorNote>Could not send</ErrorNote>)
    expect(screen.getByRole('alert').textContent).toBe('Could not send')
  })

  it('accepts a className', () => {
    render(<ErrorNote className="mt-2">x</ErrorNote>)
    expect(screen.getByRole('alert').className).toContain('mt-2')
  })
})
