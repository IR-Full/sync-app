import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { Button } from './button'

describe('Button', () => {
  it('renders its children', () => {
    render(<Button>Send</Button>)
    expect(screen.getByRole('button', { name: 'Send' })).toBeDefined()
  })

  /**
   * A bare `<button>` defaults to `type="submit"`, so one dropped inside a form
   * submits it by accident — which, in a login form, means a half-filled
   * submission on any stray click. Opting out by default makes submitting
   * something a button has to ask for explicitly.
   */
  it('defaults to type="button", not submit', () => {
    render(<Button>Cancel</Button>)
    expect(screen.getByRole('button').getAttribute('type')).toBe('button')
  })

  it('still allows an explicit submit button', () => {
    render(<Button type="submit">Sign in</Button>)
    expect(screen.getByRole('button').getAttribute('type')).toBe('submit')
  })

  it('calls onClick', () => {
    const onClick = vi.fn()
    render(<Button onClick={onClick}>Send</Button>)

    fireEvent.click(screen.getByRole('button'))
    expect(onClick).toHaveBeenCalledOnce()
  })

  it('disables while loading', () => {
    // Otherwise a double-click sends the message twice while the first request
    // is still in flight.
    render(<Button loading>Send</Button>)
    expect((screen.getByRole('button') as HTMLButtonElement).disabled).toBe(true)
  })

  it('announces the busy state to assistive tech', () => {
    render(<Button loading>Send</Button>)
    expect(screen.getByRole('button').getAttribute('aria-busy')).toBe('true')
  })

  it('omits aria-busy when idle rather than setting it false', () => {
    render(<Button>Send</Button>)
    expect(screen.getByRole('button').getAttribute('aria-busy')).toBeNull()
  })

  it('does not fire onClick while loading', () => {
    const onClick = vi.fn()
    render(
      <Button loading onClick={onClick}>
        Send
      </Button>,
    )

    fireEvent.click(screen.getByRole('button'))
    expect(onClick).not.toHaveBeenCalled()
  })

  it('respects an explicit disabled even without loading', () => {
    render(<Button disabled>Send</Button>)
    expect((screen.getByRole('button') as HTMLButtonElement).disabled).toBe(true)
  })

  it('stays disabled when loading is combined with disabled={false}', () => {
    render(
      <Button loading disabled={false}>
        Send
      </Button>,
    )
    expect((screen.getByRole('button') as HTMLButtonElement).disabled).toBe(true)
  })

  it('shows a spinner while loading and hides it from screen readers', () => {
    // The button already announces itself through aria-busy; the spinner would
    // only add an unlabelled node to the accessibility tree.
    const { container } = render(<Button loading>Send</Button>)
    expect(container.querySelector('[aria-hidden]')).not.toBeNull()
  })

  it('shows no spinner when idle', () => {
    const { container } = render(<Button>Send</Button>)
    expect(container.querySelector('[aria-hidden]')).toBeNull()
  })

  it('keeps its label visible while loading', () => {
    // Replacing the text with a spinner would make the button change width and
    // lose the only clue about what it does.
    render(<Button loading>Send</Button>)
    expect(screen.getByRole('button', { name: /Send/ })).toBeDefined()
  })

  it('renders each variant with distinct classes', () => {
    const classesFor = (variant: 'primary' | 'secondary' | 'ghost' | 'danger') => {
      const { container, unmount } = render(<Button variant={variant}>x</Button>)
      const className = container.querySelector('button')!.className
      unmount()
      return className
    }

    const all = (['primary', 'secondary', 'ghost', 'danger'] as const).map(classesFor)
    expect(new Set(all).size).toBe(all.length)
  })

  it('renders each size with distinct classes', () => {
    const classesFor = (size: 'small' | 'medium') => {
      const { container, unmount } = render(<Button size={size}>x</Button>)
      const className = container.querySelector('button')!.className
      unmount()
      return className
    }

    expect(classesFor('small')).not.toBe(classesFor('medium'))
  })

  it('appends the caller`s className last so it can override', () => {
    // The module composes classes in one direction and relies on CSS source
    // order for overrides; a caller class placed first would never win.
    const { container } = render(<Button className="custom-class">x</Button>)
    expect(container.querySelector('button')!.className).toMatch(/custom-class$/)
  })

  it('forwards arbitrary button attributes', () => {
    render(<Button aria-label="Close dialog" data-testid="x" />)
    expect(screen.getByLabelText('Close dialog')).toBeDefined()
  })

  it('renders without children', () => {
    // Icon-only buttons pass an aria-label and no text.
    expect(() => render(<Button aria-label="Close" />)).not.toThrow()
  })
})
