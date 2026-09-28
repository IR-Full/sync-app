import { describe, expect, it } from 'vitest'

import { cn } from './cn'

describe('cn', () => {
  it('joins class names with a single space', () => {
    expect(cn('a', 'b', 'c')).toBe('a b c')
  })

  /**
   * Every call site is of the form `cn('base', condition && 'variant')`. If the
   * falsy branch leaked through, React would render `class="base false"` and
   * Tailwind would emit no rule for it — a silently missing style rather than a
   * crash, which is exactly the kind of bug that survives review.
   */
  it('drops false, null and undefined', () => {
    expect(cn('base', false, null, undefined, 'active')).toBe('base active')
  })

  it('drops the empty string rather than emitting a double space', () => {
    expect(cn('a', '', 'b')).toBe('a b')
  })

  it('returns an empty string when everything is falsy', () => {
    // `className={cn(...)}` with an empty result renders no class attribute,
    // which is what an unstyled element should look like.
    expect(cn(false, null, undefined)).toBe('')
  })

  it('returns an empty string for no arguments', () => {
    expect(cn()).toBe('')
  })

  it('preserves order so a caller override wins by CSS source order', () => {
    // The module deliberately does not resolve Tailwind conflicts; it relies on
    // the caller's class coming last. Reordering would break every override.
    expect(cn('p-2 text-sm', 'p-4')).toBe('p-2 text-sm p-4')
  })
})
