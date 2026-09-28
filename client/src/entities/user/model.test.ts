import { beforeEach, describe, expect, it } from 'vitest'

import { labelForUser, useUserDirectory, type KnownUser } from './model'

const store = () => useUserDirectory.getState()

beforeEach(() => store().clear())

describe('upsert', () => {
  it('adds a user', () => {
    store().upsert({ userId: 'u1', username: 'alice' })
    expect(store().users.u1).toMatchObject({ userId: 'u1', username: 'alice' })
  })

  /**
   * Labels are assembled from several sources — the contact list carries names,
   * typed handles carry usernames, presence frames carry neither. A wholesale
   * replace would make a presence update erase the name the contact sync had
   * just supplied.
   */
  it('merges into an existing record instead of replacing it', () => {
    store().upsert({ userId: 'u1', username: 'alice' })
    store().upsert({ userId: 'u1', name: 'Alice Smith' })

    expect(store().users.u1).toMatchObject({ username: 'alice', name: 'Alice Smith' })
  })

  it('overwrites a field with a newer value', () => {
    store().upsert({ userId: 'u1', name: 'Old' })
    store().upsert({ userId: 'u1', name: 'New' })
    expect(store().users.u1.name).toBe('New')
  })

  it('keeps users independent', () => {
    store().upsert({ userId: 'u1', name: 'Alice' })
    store().upsert({ userId: 'u2', name: 'Bob' })

    expect(store().users.u1.name).toBe('Alice')
    expect(store().users.u2.name).toBe('Bob')
  })
})

describe('upsertMany', () => {
  it('adds several users at once', () => {
    store().upsertMany([
      { userId: 'u1', name: 'Alice' },
      { userId: 'u2', name: 'Bob' },
    ])
    expect(Object.keys(store().users).sort()).toEqual(['u1', 'u2'])
  })

  it('merges each entry rather than replacing', () => {
    // A contact sync carries names but no presence; the online flag from an
    // earlier PRESENCE frame has to survive it.
    store().upsert({ userId: 'u1', online: true })
    store().upsertMany([{ userId: 'u1', name: 'Alice' }])

    expect(store().users.u1).toMatchObject({ online: true, name: 'Alice' })
  })

  it('accepts an empty batch', () => {
    store().upsert({ userId: 'u1', name: 'Alice' })
    store().upsertMany([])
    expect(store().users.u1.name).toBe('Alice')
  })

  it('applies later entries over earlier ones within one batch', () => {
    store().upsertMany([
      { userId: 'u1', name: 'First' },
      { userId: 'u1', name: 'Second' },
    ])
    expect(store().users.u1.name).toBe('Second')
  })
})

describe('setPresence', () => {
  it('records online state and the last-seen stamp', () => {
    store().setPresence('u1', true, 1_700_000)
    expect(store().users.u1).toMatchObject({ online: true, lastSeenMs: 1_700_000 })
  })

  it('creates a record for a user we have never seen', () => {
    // A PRESENCE frame can be the first thing we learn about someone in a group.
    store().setPresence('u9', false, 5)
    expect(store().users.u9.userId).toBe('u9')
  })

  it('keeps the name a contact sync supplied', () => {
    store().upsert({ userId: 'u1', name: 'Alice' })
    store().setPresence('u1', true, 1)
    expect(store().users.u1.name).toBe('Alice')
  })

  it('flips a user offline', () => {
    store().setPresence('u1', true, 1)
    store().setPresence('u1', false, 2)
    expect(store().users.u1).toMatchObject({ online: false, lastSeenMs: 2 })
  })
})

describe('labelForUser', () => {
  /**
   * The order matters: an address-book name is what the user chose to call this
   * person, a handle is what the server calls them, and the short id is a last
   * resort. Rendering an empty string — which a bare `user.name` would do for
   * anyone not in the contact list — leaves an anonymous bubble in a group chat.
   */
  it('prefers the contact name', () => {
    expect(labelForUser({ userId: 'u1', name: 'Alice', username: 'alice' }, 'u1')).toBe('Alice')
  })

  it('falls back to the handle', () => {
    expect(labelForUser({ userId: 'u1', username: 'alice' }, 'u1')).toBe('@alice')
  })

  it('does not double the @ on a handle that already has one', () => {
    expect(labelForUser({ userId: 'u1', username: '@alice' }, 'u1')).toBe('@alice')
  })

  it('falls back to a short id for an unknown user', () => {
    expect(labelForUser(undefined, 'abcdef123456')).toBe('#123456')
  })

  it('falls back to a short id for a record with no labels', () => {
    expect(labelForUser({ userId: 'abcdef123456' }, 'abcdef123456')).toBe('#123456')
  })

  it('handles an id shorter than the six characters it trims to', () => {
    expect(labelForUser(undefined, 'ab')).toBe('#ab')
  })

  it('never returns an empty string', () => {
    for (const user of [
      undefined,
      { userId: 'u1' },
      { userId: 'u1', name: '' },
      { userId: 'u1', username: '' },
    ] as (KnownUser | undefined)[]) {
      expect(labelForUser(user, 'u1').length).toBeGreaterThan(0)
    }
  })

  it('ignores an empty name and uses the handle', () => {
    expect(labelForUser({ userId: 'u1', name: '', username: 'alice' }, 'u1')).toBe('@alice')
  })
})

describe('clear', () => {
  it('empties the directory', () => {
    store().upsert({ userId: 'u1', name: 'Alice' })
    store().clear()
    expect(store().users).toEqual({})
  })

  it('is what stops one account seeing another account`s contact names', () => {
    store().upsert({ userId: 'u1', name: 'Alice' })
    store().clear()
    expect(labelForUser(store().users.u1, 'u1')).toBe('#u1')
  })
})
