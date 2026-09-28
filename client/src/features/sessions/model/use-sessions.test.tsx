import { act, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useSessionStore } from '@/entities/session'
import { MsgType } from '@/shared/api'
import {
  flush,
  installFakeSocket,
  renderHookWithProvider,
  replyToLast,
} from '@/shared/api/syncapp/test-harness'
import { useDeleteAccount, useRevokeSession, useSessions } from './use-sessions'

const SELF = {
  userId: 'self',
  username: 'alice',
  deviceId: 'dev-1',
  sessionId: 's1',
  token: 'tok-1',
  resumeToken: 'resume-1',
}

const SESSIONS = {
  sessions: [
    {
      sessionId: 's1',
      deviceId: 'dev-1',
      platform: 'web',
      createdAt: 1_000,
      expiresAt: 9_000,
      current: true,
    },
    {
      sessionId: 's2',
      deviceId: 'dev-2',
      platform: 'android',
      createdAt: 2_000,
      expiresAt: 9_000,
      current: false,
    },
  ],
}

beforeEach(() => {
  installFakeSocket()
  localStorage.clear()
  vi.spyOn(console, 'error').mockImplementation(() => {})
  useSessionStore.setState({ status: 'authenticated', session: SELF })
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('useSessions', () => {
  it('asks for the list and maps every field the UI needs', async () => {
    const harness = await renderHookWithProvider(() => useSessions())
    await harness.connect()
    await act(async () => {
      replyToLast(harness.socket, MsgType.SESSIONS, SESSIONS)
    })

    await waitFor(() => expect(harness.result.current.data).toHaveLength(2))
    const asked = harness.socket.frames.filter((f) => f.type === MsgType.SESSION_LIST)
    expect(asked).toHaveLength(1)

    const [first] = harness.result.current.data!
    expect(first).toMatchObject({ sessionId: 's1', platform: 'web', current: true })
  })

  /**
   * The current session leads the list because it is the row a reader is looking
   * for in order to AVOID it — every other row signs out a device they may still
   * want, and this one signs out the browser they are reading in.
   */
  it('puts the current session first', async () => {
    const reversed = { sessions: [...SESSIONS.sessions].reverse() }
    const harness = await renderHookWithProvider(() => useSessions())
    await harness.connect()
    await act(async () => {
      replyToLast(harness.socket, MsgType.SESSIONS, reversed)
    })

    await waitFor(() => expect(harness.result.current.data).toHaveLength(2))
    expect(harness.result.current.data![0].current).toBe(true)
  })
})

describe('useRevokeSession', () => {
  it('revokes one session by id', async () => {
    const harness = await renderHookWithProvider(() => useRevokeSession())
    await harness.connect()

    act(() => harness.result.current.mutate('s2'))
    await act(async () => {
      await flush()
      replyToLast(harness.socket, MsgType.SESSION_REVOKED, { revoked: 1, self: false })
    })

    const frame = harness.socket.frames.find((f) => f.type === MsgType.SESSION_REVOKE)
    expect(frame?.body).toMatchObject({ sessionId: 's2', allIncludingCurrent: false })
  })

  /**
   * "Everywhere else" is an EMPTY session id, not a separate message and not
   * `allIncludingCurrent`. Getting this wrong signs the user out of the device
   * they are using to secure the account, which is the opposite of the intent.
   */
  it('sends an empty id for "sign out everywhere else", sparing this device', async () => {
    const harness = await renderHookWithProvider(() => useRevokeSession())
    await harness.connect()

    act(() => harness.result.current.mutate(null))
    await act(async () => {
      await flush()
      replyToLast(harness.socket, MsgType.SESSION_REVOKED, { revoked: 3, self: false })
    })

    const frame = harness.socket.frames.find((f) => f.type === MsgType.SESSION_REVOKE)
    expect(frame?.body).toMatchObject({ sessionId: '', allIncludingCurrent: false })
    // The session survives: nothing was cleared.
    expect(useSessionStore.getState().session).not.toBeNull()
  })

  /**
   * When the server reports it killed OUR session, the socket is already closing
   * — so the client has to drop the token itself rather than wait for a reply
   * that will never arrive.
   */
  it('clears the local session when the server says it revoked ours', async () => {
    const harness = await renderHookWithProvider(() => useRevokeSession())
    await harness.connect()

    act(() => harness.result.current.mutate('s1'))
    await act(async () => {
      await flush()
      replyToLast(harness.socket, MsgType.SESSION_REVOKED, { revoked: 1, self: true })
    })

    await waitFor(() => expect(useSessionStore.getState().session).toBeNull())
    expect(useSessionStore.getState().status).toBe('anonymous')
  })
})

describe('useDeleteAccount', () => {
  it('sends the re-confirmed password', async () => {
    const harness = await renderHookWithProvider(() => useDeleteAccount())
    await harness.connect()

    act(() => harness.result.current.mutate({ password: 'secret123' }))
    await act(async () => {
      await flush()
      replyToLast(harness.socket, MsgType.ACCOUNT_DELETED, {
        userId: 'self',
        deletedAt: 1_700_000_000_000,
      })
    })

    const frame = harness.socket.frames.find((f) => f.type === MsgType.ACCOUNT_DELETE)
    expect(frame?.body).toMatchObject({ password: 'secret123' })
  })

  it('drops the local session once the account is gone', async () => {
    const harness = await renderHookWithProvider(() => useDeleteAccount())
    await harness.connect()

    act(() => harness.result.current.mutate({ password: 'secret123' }))
    await act(async () => {
      await flush()
      replyToLast(harness.socket, MsgType.ACCOUNT_DELETED, { userId: 'self', deletedAt: 1 })
    })

    await waitFor(() => expect(useSessionStore.getState().session).toBeNull())
  })

  /**
   * A rejected password must leave the session alone. Clearing it on any failure
   * would log someone out for mistyping, on the screen where they are trying to
   * prove they are themselves.
   */
  it('keeps the session when the password is refused', async () => {
    const harness = await renderHookWithProvider(() => useDeleteAccount())
    await harness.connect()

    act(() => harness.result.current.mutate({ password: 'wrong' }))
    await act(async () => {
      await flush()
      replyToLast(harness.socket, MsgType.ERROR, {
        code: 3000,
        message: 'password does not match',
      })
    })

    await waitFor(() => expect(harness.result.current.isError).toBe(true))
    expect(useSessionStore.getState().session).not.toBeNull()
  })
})
