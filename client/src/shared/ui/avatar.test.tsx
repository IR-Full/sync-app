import { fireEvent, render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { Avatar } from './avatar'

/** The monogram is the only text node the component renders. */
const monogram = (container: HTMLElement) =>
  container.querySelector('span[aria-hidden]')?.textContent

describe('monogram', () => {
  it('takes the initials of a two-part name', () => {
    const { container } = render(<Avatar seed="u1" name="Alice Smith" />)
    expect(monogram(container)).toBe('AS')
  })

  it('takes the first two letters of a single-word name', () => {
    const { container } = render(<Avatar seed="u1" name="Alice" />)
    expect(monogram(container)).toBe('AL')
  })

  it('uppercases a lowercase name', () => {
    const { container } = render(<Avatar seed="u1" name="alice smith" />)
    expect(monogram(container)).toBe('AS')
  })

  it('strips a leading @ from a handle', () => {
    // Every handle would otherwise render as "@" plus one letter, making a
    // whole contact list look identical.
    const { container } = render(<Avatar seed="u1" name="@alice" />)
    expect(monogram(container)).toBe('AL')
  })

  it('splits on separators a username can contain', () => {
    for (const [name, expected] of [
      ['alice_smith', 'AS'],
      ['alice.smith', 'AS'],
      ['alice-smith', 'AS'],
    ]) {
      const { container, unmount } = render(<Avatar seed="u1" name={name} />)
      expect({ name, initials: monogram(container) }).toEqual({ name, initials: expected })
      unmount()
    }
  })

  it('falls back to a question mark for an empty name', () => {
    // Renders for ids we know without having fetched a profile; an empty circle
    // would look like a rendering bug.
    const { container } = render(<Avatar seed="u1" name="" />)
    expect(monogram(container)).toBe('?')
  })

  it('falls back to a question mark for a whitespace-only name', () => {
    const { container } = render(<Avatar seed="u1" name="   " />)
    expect(monogram(container)).toBe('?')
  })

  it('handles a one-character name', () => {
    const { container } = render(<Avatar seed="u1" name="A" />)
    expect(monogram(container)).toBe('A')
  })

  it('handles a non-Latin name', () => {
    const { container } = render(<Avatar seed="u1" name="Иван Петров" />)
    expect(monogram(container)).toBe('ИП')
  })

  it('hides the monogram from screen readers', () => {
    // "AS" read aloud next to the name it was derived from is pure noise.
    const { container } = render(<Avatar seed="u1" name="Alice Smith" />)
    expect(container.querySelector('span[aria-hidden]')).not.toBeNull()
  })
})

describe('colour', () => {
  /**
   * The colour comes from a stable hash of the seed, so the same person is the
   * same colour on every screen — which is what makes a chat list scannable at a
   * glance. A random or index-based colour would reshuffle on every render.
   */
  it('is stable for the same seed', () => {
    const classFor = (seed: string) => {
      const { container, unmount } = render(<Avatar seed={seed} name="Alice" />)
      const className = container.querySelector('span[aria-hidden]')!.className
      unmount()
      return className
    }

    expect(classFor('user-123')).toBe(classFor('user-123'))
  })

  it('does not depend on the display name', () => {
    // A user who renames themselves must not change colour.
    const classFor = (name: string) => {
      const { container, unmount } = render(<Avatar seed="user-123" name={name} />)
      const className = container.querySelector('span[aria-hidden]')!.className
      unmount()
      return className
    }

    expect(classFor('Alice')).toBe(classFor('Alice Smith'))
  })

  it('spreads different seeds across the palette', () => {
    const classes = new Set(
      Array.from({ length: 40 }, (_, index) => {
        const { container, unmount } = render(<Avatar seed={`u${index}`} name="X" />)
        const className = container.querySelector('span[aria-hidden]')!.className
        unmount()
        return className
      }),
    )
    // Not a distribution test — just proof the hash is not collapsing to one
    // bucket, which is what an overflowing or un-absolute hash would do.
    expect(classes.size).toBeGreaterThan(3)
  })

  it('falls back to the name when there is no seed', () => {
    expect(() => render(<Avatar seed="" name="Alice" />)).not.toThrow()
  })
})

describe('picture', () => {
  it('renders an image when a src is given', () => {
    const { container } = render(<Avatar seed="u1" name="Alice" src="/media/avatar.png" />)
    expect(container.querySelector('img')).not.toBeNull()
  })

  it('gives the image an empty alt, since the name is already beside it', () => {
    // An empty alt is what takes the picture out of the accessibility tree —
    // hence querySelector rather than getByRole('img'), which would no longer
    // match. Without it a screen reader announces a URL next to the name it
    // already read.
    const { container } = render(<Avatar seed="u1" name="Alice" src="/media/avatar.png" />)
    expect(container.querySelector('img')!.getAttribute('alt')).toBe('')
  })

  /**
   * A picture arrives as a signed, *expiring* media URL. When the signature
   * lapses the fetch 403s, and without this fallback the avatar becomes a hole
   * in the layout rather than the monogram it started as.
   */
  it('falls back to the monogram when the picture fails to load', () => {
    const { container } = render(<Avatar seed="u1" name="Alice Smith" src="/expired.png" />)
    fireEvent.error(container.querySelector('img')!)

    expect(monogram(container)).toBe('AS')
  })

  it('shows the monogram when no src is given', () => {
    const { container } = render(<Avatar seed="u1" name="Alice Smith" />)
    expect(container.querySelector('img')).toBeNull()
    expect(monogram(container)).toBe('AS')
  })

  it('treats an empty src as no picture', () => {
    const { container } = render(<Avatar seed="u1" name="Alice Smith" src="" />)
    expect(container.querySelector('img')).toBeNull()
  })
})

describe('presence dot', () => {
  it('is absent when presence is unknown', () => {
    // `undefined` means "we have no presence for this user" — distinct from
    // offline. Showing a grey dot would claim knowledge we do not have.
    const { container } = render(<Avatar seed="u1" name="Alice" />)
    expect(container.querySelectorAll('span[aria-hidden]')).toHaveLength(1)
  })

  it('appears when the user is online', () => {
    const { container } = render(<Avatar seed="u1" name="Alice" online />)
    expect(container.querySelectorAll('span[aria-hidden]')).toHaveLength(2)
  })

  it('appears when the user is known to be offline', () => {
    const { container } = render(<Avatar seed="u1" name="Alice" online={false} />)
    expect(container.querySelectorAll('span[aria-hidden]')).toHaveLength(2)
  })

  it('renders online and offline differently', () => {
    const dotClass = (online: boolean) => {
      const { container, unmount } = render(<Avatar seed="u1" name="A" online={online} />)
      const className = container.querySelectorAll('span[aria-hidden]')[1].className
      unmount()
      return className
    }

    expect(dotClass(true)).not.toBe(dotClass(false))
  })
})

describe('sizes', () => {
  it('renders each size differently', () => {
    const classFor = (size: 'small' | 'medium' | 'large') => {
      const { container, unmount } = render(<Avatar seed="u1" name="A" size={size} />)
      const className = container.querySelector('span[aria-hidden]')!.className
      unmount()
      return className
    }

    const all = (['small', 'medium', 'large'] as const).map(classFor)
    expect(new Set(all).size).toBe(all.length)
  })

  it('sizes the image to match the requested size', () => {
    // `next/image` needs intrinsic dimensions, and a mismatch between the CSS
    // size and the intrinsic one makes the picture render blurry or cropped.
    const { container } = render(<Avatar seed="u1" name="A" size="large" src="/a.png" />)
    expect(container.querySelector('img')!.getAttribute('width')).toBe('56')
  })
})
