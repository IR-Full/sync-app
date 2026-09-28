import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { TextField } from './field'

describe('TextField', () => {
  it('renders an input', () => {
    render(<TextField />)
    expect(screen.getByRole('textbox')).toBeDefined()
  })

  /**
   * The label is wired with a generated `useId`, not a caller-supplied one, so
   * two fields on one form cannot collide. If the wiring broke, the field would
   * still look right and be unreachable by label — for a screen reader and for
   * `getByLabelText` alike.
   */
  it('associates the label with the input', () => {
    render(<TextField label="Username" />)
    expect(screen.getByLabelText('Username')).toBe(screen.getByRole('textbox'))
  })

  it('gives two fields on one form distinct ids', () => {
    render(
      <>
        <TextField label="Username" />
        <TextField label="Password" />
      </>,
    )
    const [first, second] = screen.getAllByRole('textbox')
    expect(first.id).not.toBe(second.id)
    expect(first.id).toBeTruthy()
  })

  it('renders without a label', () => {
    expect(() => render(<TextField placeholder="Search" />)).not.toThrow()
  })

  it('shows a hint', () => {
    render(<TextField label="Username" hint="Letters and digits, no @" />)
    expect(screen.getByText('Letters and digits, no @')).toBeDefined()
  })

  it('points aria-describedby at the hint', () => {
    // Otherwise the rule ("at least 8 characters") is visible on screen and
    // invisible to anyone using a screen reader.
    render(<TextField label="Password" hint="At least 8 characters" />)
    const input = screen.getByRole('textbox')
    const hintId = input.getAttribute('aria-describedby')

    expect(hintId).toBeTruthy()
    expect(document.getElementById(hintId!)?.textContent).toBe('At least 8 characters')
  })

  it('shows an error', () => {
    render(<TextField label="Username" error="Already taken" />)
    expect(screen.getByText('Already taken')).toBeDefined()
  })

  it('marks the input invalid when there is an error', () => {
    render(<TextField label="Username" error="Already taken" />)
    expect(screen.getByRole('textbox').getAttribute('aria-invalid')).toBe('true')
  })

  it('omits aria-invalid when valid rather than setting it false', () => {
    render(<TextField label="Username" />)
    expect(screen.getByRole('textbox').getAttribute('aria-invalid')).toBeNull()
  })

  it('points aria-describedby at the error, not the hint', () => {
    // The error is the actionable message; describing the field by a stale hint
    // would announce the rule the user has already broken instead of the reason.
    render(<TextField label="Username" hint="No @ sign" error="Already taken" />)
    const describedBy = screen.getByRole('textbox').getAttribute('aria-describedby')

    expect(document.getElementById(describedBy!)?.textContent).toBe('Already taken')
  })

  it('hides the hint while an error is showing', () => {
    render(<TextField label="Username" hint="No @ sign" error="Already taken" />)
    expect(screen.queryByText('No @ sign')).toBeNull()
  })

  it('treats a null error as no error', () => {
    // Call sites hold `error: string | null` from a mutation result.
    render(<TextField label="Username" hint="No @ sign" error={null} />)
    expect(screen.getByText('No @ sign')).toBeDefined()
    expect(screen.getByRole('textbox').getAttribute('aria-invalid')).toBeNull()
  })

  it('treats an empty error string as no error', () => {
    render(<TextField label="Username" error="" />)
    expect(screen.getByRole('textbox').getAttribute('aria-invalid')).toBeNull()
  })

  it('omits aria-describedby when there is neither hint nor error', () => {
    render(<TextField label="Username" />)
    expect(screen.getByRole('textbox').getAttribute('aria-describedby')).toBeNull()
  })

  it('renders a prefix sigil', () => {
    render(<TextField label="Username" prefix="@" />)
    expect(screen.getByText('@')).toBeDefined()
  })

  it('reports value changes', () => {
    const onChange = vi.fn()
    render(<TextField label="Username" onChange={onChange} />)

    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'alice' } })
    expect(onChange).toHaveBeenCalled()
  })

  it('forwards native input attributes', () => {
    render(<TextField label="Password" type="password" maxLength={64} required />)
    const input = screen.getByLabelText('Password') as HTMLInputElement

    expect(input.type).toBe('password')
    expect(input.maxLength).toBe(64)
    expect(input.required).toBe(true)
  })

  it('supports being disabled', () => {
    render(<TextField label="Username" disabled />)
    expect((screen.getByRole('textbox') as HTMLInputElement).disabled).toBe(true)
  })

  it('appends the caller`s className last so it can override', () => {
    render(<TextField label="Username" className="custom-class" />)
    expect(screen.getByRole('textbox').className).toMatch(/custom-class$/)
  })
})
