import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { Toggle } from './toggle'

const noop = () => {}

describe('Toggle', () => {
  /**
   * It is a `<button role="switch">`, not a checkbox, so none of the accessible
   * state comes for free: `aria-checked` is the only thing telling a screen
   * reader whether the setting is on. A styled div that merely looks like a
   * switch is the classic version of this bug.
   */
  it('exposes itself as a switch', () => {
    render(<Toggle checked={false} onChange={noop} label="Sound" />)
    expect(screen.getByRole('switch')).toBeDefined()
  })

  it('reports the on state', () => {
    render(<Toggle checked onChange={noop} label="Sound" />)
    expect(screen.getByRole('switch').getAttribute('aria-checked')).toBe('true')
  })

  it('reports the off state', () => {
    render(<Toggle checked={false} onChange={noop} label="Sound" />)
    expect(screen.getByRole('switch').getAttribute('aria-checked')).toBe('false')
  })

  it('is reachable by its label', () => {
    render(<Toggle checked={false} onChange={noop} label="Sound on message" />)
    expect(screen.getByLabelText('Sound on message')).toBe(screen.getByRole('switch'))
  })

  it('gives two toggles on one panel distinct ids', () => {
    render(
      <>
        <Toggle checked={false} onChange={noop} label="Sound" />
        <Toggle checked={false} onChange={noop} label="Notifications" />
      </>,
    )
    const [first, second] = screen.getAllByRole('switch')
    expect(first.id).not.toBe(second.id)
  })

  it('reports the opposite value when clicked on', () => {
    const onChange = vi.fn()
    render(<Toggle checked={false} onChange={onChange} label="Sound" />)

    fireEvent.click(screen.getByRole('switch'))
    expect(onChange).toHaveBeenCalledWith(true)
  })

  it('reports the opposite value when clicked off', () => {
    const onChange = vi.fn()
    render(<Toggle checked onChange={onChange} label="Sound" />)

    fireEvent.click(screen.getByRole('switch'))
    expect(onChange).toHaveBeenCalledWith(false)
  })

  it('is controlled — it does not flip itself', () => {
    // The parent owns the value (it persists it); a self-flipping switch would
    // show "on" after a failed write.
    const onChange = vi.fn()
    render(<Toggle checked={false} onChange={onChange} label="Sound" />)

    fireEvent.click(screen.getByRole('switch'))
    expect(screen.getByRole('switch').getAttribute('aria-checked')).toBe('false')
  })

  it('renders an optional description', () => {
    render(
      <Toggle
        checked={false}
        onChange={noop}
        label="Read receipts"
        description="Others stop seeing your ticks"
      />,
    )
    expect(screen.getByText('Others stop seeing your ticks')).toBeDefined()
  })

  it('renders without a description', () => {
    expect(() => render(<Toggle checked={false} onChange={noop} label="Sound" />)).not.toThrow()
  })

  it('can be disabled', () => {
    render(<Toggle checked={false} onChange={noop} label="Sound" disabled />)
    expect((screen.getByRole('switch') as HTMLButtonElement).disabled).toBe(true)
  })

  it('does not report a change while disabled', () => {
    const onChange = vi.fn()
    render(<Toggle checked={false} onChange={onChange} label="Sound" disabled />)

    fireEvent.click(screen.getByRole('switch'))
    expect(onChange).not.toHaveBeenCalled()
  })

  it('is a type="button" so it cannot submit a surrounding form', () => {
    render(<Toggle checked={false} onChange={noop} label="Sound" />)
    expect(screen.getByRole('switch').getAttribute('type')).toBe('button')
  })

  it('accepts a rich label node', () => {
    render(<Toggle checked={false} onChange={noop} label={<span>Rich label</span>} />)
    expect(screen.getByText('Rich label')).toBeDefined()
  })
})
