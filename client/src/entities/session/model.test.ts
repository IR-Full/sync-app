import { beforeEach, describe, expect, it, vi } from 'vitest'

import { StorageKeys, readStorage } from '@/shared/lib/storage'
import { useSessionStore, type StoredSession } from './model'

const session = (overrides: Partial<StoredSession> = {}): StoredSession => ({
  userId: 'u1',
  username: 'alice',
  deviceId: 'dev-1',
  sessionId: 's1',
  token: 'tok-1',
  resumeToken: 'resume-1',
  displayName: 'Alice',
  avatarRef: '',
  ...overrides,
})

const store = () => useSessionStore.getState()

beforeEach(() => {
  localStorage.clear()
  useSessionStore.setState({ status: 'unknown', session: null })
  vi.restoreAllMocks()
})

describe('initial state', () => {
  /**
   * `unknown` is not `anonymous`: the router must not bounce a returning user to
   * the login screen during the render before `hydrate()` has looked at storage.
   * Defaulting to `anonymous` would flash the login form on every reload.
   */
  it('starts as unknown, not anonymous', () => {
    expect(store().status).toBe('unknown')
    expect(store().session).toBeNull()
  })
})

describe('setSession', () => {
  it('stores the session and marks the user authenticated', () => {
    store().setSession(session())
    expect(store().status).toBe('authenticated')
    expect(store().session).toMatchObject({ userId: 'u1', token: 'tok-1' })
  })

  it('persists so the session survives a reload', () => {
    store().setSession(session())
    expect(readStorage<StoredSession | null>(StorageKeys.session, null)).toMatchObject({
      userId: 'u1',
      token: 'tok-1',
    })
  })

  it('replaces an earlier session wholesale', () => {
    // Switching accounts must not leave the previous user's resume token behind.
    store().setSession(session({ userId: 'u1', resumeToken: 'old' }))
    store().setSession(session({ userId: 'u2', resumeToken: 'new' }))

    expect(store().session).toMatchObject({ userId: 'u2', resumeToken: 'new' })
  })

  it('survives a storage failure without losing the in-memory session', () => {
    // Private mode or an exhausted quota should cost persistence, not the login.
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('QuotaExceededError')
    })

    expect(() => store().setSession(session())).not.toThrow()
    expect(store().status).toBe('authenticated')
  })
})

describe('updateSession', () => {
  it('patches individual fields', () => {
    store().setSession(session())
    store().updateSession({ displayName: 'Alice B.' })

    expect(store().session).toMatchObject({ displayName: 'Alice B.', userId: 'u1' })
  })

  it('persists the patch', () => {
    store().setSession(session())
    store().updateSession({ avatarRef: 'media-1' })

    expect(readStorage<StoredSession | null>(StorageKeys.session, null)?.avatarRef).toBe(
      'media-1',
    )
  })

  it('leaves untouched fields alone', () => {
    // PROFILE frames mirrored from this account's other devices carry only the
    // profile; a wholesale replace would drop the token and log the tab out.
    store().setSession(session())
    store().updateSession({ displayName: 'Alice B.' })

    expect(store().session?.token).toBe('tok-1')
    expect(store().session?.resumeToken).toBe('resume-1')
  })

  it('does nothing when there is no session', () => {
    // A PROFILE frame can race a logout.
    expect(() => store().updateSession({ displayName: 'ghost' })).not.toThrow()
    expect(store().session).toBeNull()
  })

  it('does not resurrect a cleared session', () => {
    store().setSession(session())
    store().clear()
    store().updateSession({ displayName: 'ghost' })

    expect(store().session).toBeNull()
    expect(store().status).toBe('anonymous')
  })
})

describe('clear', () => {
  it('drops the session and marks the user anonymous', () => {
    store().setSession(session())
    store().clear()

    expect(store().session).toBeNull()
    expect(store().status).toBe('anonymous')
  })

  it('removes the token from storage', () => {
    // A bearer token left in localStorage after logout is exactly what an XSS
    // on a shared machine would pick up.
    store().setSession(session())
    store().clear()

    expect(localStorage.getItem(StorageKeys.session)).toBeNull()
  })

  it('is safe with no session', () => {
    expect(() => store().clear()).not.toThrow()
    expect(store().status).toBe('anonymous')
  })
})

describe('hydrate', () => {
  it('restores a stored session', () => {
    localStorage.setItem(StorageKeys.session, JSON.stringify(session()))
    store().hydrate()

    expect(store().status).toBe('authenticated')
    expect(store().session).toMatchObject({ userId: 'u1' })
  })

  it('reports anonymous when nothing is stored', () => {
    store().hydrate()
    expect(store().status).toBe('anonymous')
    expect(store().session).toBeNull()
  })

  /**
   * A half-written session is worse than none: the app would render as logged in
   * and then fail every request. Both fields are required because the reconnect
   * path needs the token and every query needs the user id.
   */
  it('rejects a stored session with no token', () => {
    localStorage.setItem(StorageKeys.session, JSON.stringify(session({ token: '' })))
    store().hydrate()
    expect(store().status).toBe('anonymous')
  })

  it('rejects a stored session with no user id', () => {
    localStorage.setItem(StorageKeys.session, JSON.stringify(session({ userId: '' })))
    store().hydrate()
    expect(store().status).toBe('anonymous')
  })

  it('rejects corrupt JSON without throwing', () => {
    localStorage.setItem(StorageKeys.session, '{not json')
    expect(() => store().hydrate()).not.toThrow()
    expect(store().status).toBe('anonymous')
  })

  it('rejects a stored value of the wrong shape', () => {
    localStorage.setItem(StorageKeys.session, JSON.stringify('a string'))
    store().hydrate()
    expect(store().status).toBe('anonymous')
  })

  it('is idempotent', () => {
    localStorage.setItem(StorageKeys.session, JSON.stringify(session()))
    store().hydrate()
    store().hydrate()
    expect(store().session).toMatchObject({ userId: 'u1' })
  })
})
