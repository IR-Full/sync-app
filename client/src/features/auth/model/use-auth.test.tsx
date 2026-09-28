import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useChatStore } from '@/entities/chat'
import { useSessionStore } from '@/entities/session'
import { useUserDirectory } from '@/entities/user'
import { ErrorCode, MsgType, queryKeys } from '@/shared/api'
import { FakeSocket } from '@/shared/api/protocol/fake-socket'
import {
  flush,
  installFakeSocket,
  renderHookWithProvider,
  replyToLast,
  type Harness,
} from '@/shared/api/syncapp/test-harness'
import { StorageKeys } from '@/shared/lib/storage'
import {
  useAuthenticate,
  useLogout,
  useRestoreSession,
  useSessionExpiryWatcher,
} from './use-auth'

const WELCOME = {
  serverVersion: 'test/1',
  sessionId: 's1',
  caps: 0b10100,
  heartbeatMs: 20_000,
  maxInflight: 32,
  resumeSupported: true,
}

const AUTH_OK = {
  userId: 'u1',
  deviceId: 'dev-1',
  sessionId: 's1',
  token: 'tok-1',
  resumeToken: 'resume-1',
  username: 'alice',
  displayName: 'Alice',
  avatarRef: 'avatar-1',
}

const STORED_SESSION = {
  userId: 'u1',
  username: 'alice',
  deviceId: 'dev-1',
  sessionId: 's1',
  token: 'stored-token',
  resumeToken: 'resume-1',
  displayName: 'Alice',
  avatarRef: '',
}

beforeEach(() => {
  installFakeSocket()
  localStorage.clear()
  vi.spyOn(console, 'error').mockImplementation(() => {})
  useSessionStore.setState({ status: 'unknown', session: null })
  useChatStore.getState().reset()
  useUserDirectory.getState().clear()
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  vi.useRealTimers()
})

/** The AUTH body the client wrote, once the handshake has got that far. */
const lastAuth = (harness: Harness<unknown>) =>
  harness.socket.frames.filter((frame) => frame.type === MsgType.AUTH).at(-1)?.body as
    Record<string, unknown> | undefined

/** Drives the gateway side of a handshake the hook has already started. */
async function completeHandshake(
  socket: FakeSocket,
  outcome: { ok: true; body?: typeof AUTH_OK } | { ok: false; code: number; message?: string },
) {
  await act(async () => {
    socket.open()
    replyToLast(socket, MsgType.WELCOME, WELCOME)
    await flush()
    if (outcome.ok) {
      replyToLast(socket, MsgType.AUTH_OK, outcome.body ?? AUTH_OK)
    } else {
      replyToLast(socket, MsgType.AUTH_ERR, {
        code: outcome.code,
        message: outcome.message ?? 'nope',
      })
    }
    await flush()
  })
}

describe('useAuthenticate', () => {
  it('starts idle', async () => {
    const harness = await renderHookWithProvider(() => useAuthenticate())
    expect(harness.result.current.pending).toBe(false)
    expect(harness.result.current.error).toBeNull()
  })

  it('signs in and stores the session', async () => {
    const harness = await renderHookWithProvider(() => useAuthenticate())

    let outcome: Promise<boolean> | undefined
    act(() => {
      outcome = harness.result.current.authenticate(
        { username: 'alice', password: 'secret' },
        false,
      )
    })
    await completeHandshake(harness.socket, { ok: true })

    expect(await outcome).toBe(true)
    expect(useSessionStore.getState().session).toMatchObject({
      userId: 'u1',
      username: 'alice',
      token: 'tok-1',
      resumeToken: 'resume-1',
      displayName: 'Alice',
      avatarRef: 'avatar-1',
    })
    expect(useSessionStore.getState().status).toBe('authenticated')
  })

  it('sends the password credential, not a token', async () => {
    const harness = await renderHookWithProvider(() => useAuthenticate())

    act(() => {
      void harness.result.current.authenticate({ username: 'alice', password: 'secret' }, false)
    })
    await act(async () => {
      harness.socket.open()
      replyToLast(harness.socket, MsgType.WELCOME, WELCOME)
      await flush()
    })

    expect(lastAuth(harness)).toMatchObject({
      username: 'alice',
      password: 'secret',
      register: false,
    })
  })

  it('flags a registration as such', async () => {
    // Sign-in and sign-up are the same AUTH frame; only this flag tells the
    // gateway to create the account rather than reject an unknown one.
    const harness = await renderHookWithProvider(() => useAuthenticate())

    act(() => {
      void harness.result.current.authenticate({ username: 'alice', password: 'secret' }, true)
    })
    await act(async () => {
      harness.socket.open()
      replyToLast(harness.socket, MsgType.WELCOME, WELCOME)
      await flush()
    })

    expect(lastAuth(harness)).toMatchObject({ register: true })
  })

  it('normalises the typed username the way the gateway does', async () => {
    // The server lowercases and strips the sigil before lookup; sending "@Alice"
    // verbatim would fail against an account stored as "alice".
    const harness = await renderHookWithProvider(() => useAuthenticate())

    act(() => {
      void harness.result.current.authenticate(
        { username: '  @alice  ', password: 'secret' },
        false,
      )
    })
    await act(async () => {
      harness.socket.open()
      replyToLast(harness.socket, MsgType.WELCOME, WELCOME)
      await flush()
    })

    expect(lastAuth(harness)).toMatchObject({ username: 'alice' })
  })

  it('prefers the username AUTH_OK reports over the one that was typed', async () => {
    // The server is authoritative about how the account is spelled.
    const harness = await renderHookWithProvider(() => useAuthenticate())

    act(() => {
      void harness.result.current.authenticate({ username: 'ALICE', password: 'secret' }, false)
    })
    await completeHandshake(harness.socket, {
      ok: true,
      body: { ...AUTH_OK, username: 'alice' },
    })

    expect(useSessionStore.getState().session?.username).toBe('alice')
  })

  it('falls back to the typed username when the gateway reports none', async () => {
    // A gateway too old to name the account in AUTH_OK still has to produce a
    // usable session rather than one with a blank name in the header.
    const harness = await renderHookWithProvider(() => useAuthenticate())

    act(() => {
      void harness.result.current.authenticate({ username: '@Bob', password: 'secret' }, false)
    })
    await completeHandshake(harness.socket, { ok: true, body: { ...AUTH_OK, username: '' } })

    expect(useSessionStore.getState().session?.username).toBe('Bob')
  })

  it('loads that account`s chat registry', async () => {
    // Chats are per-account; without this the previous user's list would still
    // be on screen after a switch.
    const harness = await renderHookWithProvider(() => useAuthenticate())

    act(() => {
      void harness.result.current.authenticate({ username: 'alice', password: 'secret' }, false)
    })
    await completeHandshake(harness.socket, { ok: true })

    expect(useChatStore.getState().ownerId).toBe('u1')
  })

  it('reports pending while the handshake is in flight', async () => {
    const harness = await renderHookWithProvider(() => useAuthenticate())

    await act(async () => {
      void harness.result.current.authenticate({ username: 'alice', password: 'x' }, false)
      await flush()
    })

    expect(harness.result.current.pending).toBe(true)
  })

  it('clears pending once the handshake settles', async () => {
    const harness = await renderHookWithProvider(() => useAuthenticate())

    act(() => {
      void harness.result.current.authenticate({ username: 'alice', password: 'x' }, false)
    })
    await completeHandshake(harness.socket, { ok: true })

    expect(harness.result.current.pending).toBe(false)
  })

  it('clears pending even when the handshake fails', async () => {
    // A stuck spinner on the login form leaves the user with no way forward.
    const harness = await renderHookWithProvider(() => useAuthenticate())

    act(() => {
      void harness.result.current.authenticate({ username: 'alice', password: 'wrong' }, false)
    })
    await completeHandshake(harness.socket, { ok: false, code: ErrorCode.UNAUTHENTICATED })

    expect(harness.result.current.pending).toBe(false)
  })

  it('returns false and stores no session on a rejection', async () => {
    const harness = await renderHookWithProvider(() => useAuthenticate())

    let outcome: Promise<boolean> | undefined
    act(() => {
      outcome = harness.result.current.authenticate(
        { username: 'alice', password: 'wrong' },
        false,
      )
    })
    await completeHandshake(harness.socket, { ok: false, code: ErrorCode.UNAUTHENTICATED })

    expect(await outcome).toBe(false)
    expect(useSessionStore.getState().session).toBeNull()
  })

  /**
   * The error text is the only thing the user has to act on, and the right
   * wording depends on which form they are looking at: the same rejection means
   * "wrong password" when signing in and "that name is taken" when registering.
   */
  it('reports bad credentials as an auth failure when signing in', async () => {
    const harness = await renderHookWithProvider(() => useAuthenticate())

    act(() => {
      void harness.result.current.authenticate({ username: 'alice', password: 'wrong' }, false)
    })
    await completeHandshake(harness.socket, { ok: false, code: ErrorCode.UNAUTHENTICATED })

    expect(harness.result.current.error).toBeTruthy()
  })

  it('reports the same rejection differently when registering', async () => {
    const harness = await renderHookWithProvider(() => useAuthenticate())

    act(() => {
      void harness.result.current.authenticate({ username: 'taken', password: 'secret' }, false)
    })
    await completeHandshake(harness.socket, { ok: false, code: ErrorCode.UNAUTHENTICATED })
    const signInMessage = harness.result.current.error

    act(() => harness.result.current.clearError())
    FakeSocket.reset()

    act(() => {
      void harness.result.current.authenticate({ username: 'taken', password: 'secret' }, true)
    })
    await completeHandshake(harness.socket, { ok: false, code: ErrorCode.UNAUTHENTICATED })

    expect(harness.result.current.error).not.toBe(signInMessage)
  })

  it('reports a throttle distinctly from a bad password', async () => {
    // "Too many attempts" and "wrong password" call for different responses —
    // waiting versus retyping — so they must not share a message.
    const harness = await renderHookWithProvider(() => useAuthenticate())

    act(() => {
      void harness.result.current.authenticate({ username: 'alice', password: 'x' }, false)
    })
    await completeHandshake(harness.socket, { ok: false, code: ErrorCode.RATE_LIMITED })

    expect(harness.result.current.error).toBeTruthy()
  })

  it('surfaces a server message for an unclassified rejection', async () => {
    // The gateway phrases its own business errors better than a generic string.
    const harness = await renderHookWithProvider(() => useAuthenticate())

    act(() => {
      void harness.result.current.authenticate({ username: 'alice', password: 'x' }, false)
    })
    await completeHandshake(harness.socket, {
      ok: false,
      code: ErrorCode.INTERNAL,
      message: 'the database is on fire',
    })

    expect(harness.result.current.error).toBe('the database is on fire')
  })

  it('clears a previous error when a new attempt starts', async () => {
    // Otherwise the stale "wrong password" stays under the form while the next
    // attempt is still running.
    const harness = await renderHookWithProvider(() => useAuthenticate())

    act(() => {
      void harness.result.current.authenticate({ username: 'alice', password: 'wrong' }, false)
    })
    await completeHandshake(harness.socket, { ok: false, code: ErrorCode.UNAUTHENTICATED })
    expect(harness.result.current.error).toBeTruthy()

    FakeSocket.reset()
    await act(async () => {
      void harness.result.current.authenticate({ username: 'alice', password: 'right' }, false)
      await flush()
    })

    expect(harness.result.current.error).toBeNull()
  })

  it('clears the error on request', async () => {
    const harness = await renderHookWithProvider(() => useAuthenticate())

    act(() => {
      void harness.result.current.authenticate({ username: 'alice', password: 'wrong' }, false)
    })
    await completeHandshake(harness.socket, { ok: false, code: ErrorCode.UNAUTHENTICATED })

    act(() => harness.result.current.clearError())

    expect(harness.result.current.error).toBeNull()
  })

  it('presents the stable device id to the gateway', async () => {
    // Sessions, multi-device delivery and E2E bundles are all keyed on it.
    const harness = await renderHookWithProvider(() => useAuthenticate())

    act(() => {
      void harness.result.current.authenticate({ username: 'alice', password: 'x' }, false)
    })
    await act(async () => {
      harness.socket.open()
      await flush()
    })

    const hello = harness.socket.frames[0].body as { deviceId: string }
    expect(hello.deviceId).toBe(JSON.parse(localStorage.getItem(StorageKeys.deviceId)!))
  })
})

describe('useRestoreSession', () => {
  it('does nothing when no session is stored', async () => {
    const harness = await renderHookWithProvider(() => useRestoreSession())

    expect(harness.result.current.restoring).toBe(false)
    expect(FakeSocket.instances).toHaveLength(0)
  })

  /**
   * "Auto login" is re-authentication with the bearer token from the last
   * AUTH_OK — the gateway accepts one in place of credentials on any new
   * connection, which is what lets a returning user skip the login form.
   */
  it('reconnects with the stored token', async () => {
    localStorage.setItem(StorageKeys.session, JSON.stringify(STORED_SESSION))
    const harness = await renderHookWithProvider(() => useRestoreSession())

    await act(async () => {
      await flush()
      harness.socket.open()
      replyToLast(harness.socket, MsgType.WELCOME, WELCOME)
      await flush()
    })

    expect(lastAuth(harness)).toMatchObject({ token: 'stored-token' })
  })

  it('reuses the stored device id rather than minting a new one', async () => {
    // A fresh id every launch would split one browser across many device rows
    // and scatter its secret-chat sessions between them.
    localStorage.setItem(StorageKeys.session, JSON.stringify(STORED_SESSION))
    const harness = await renderHookWithProvider(() => useRestoreSession())

    await act(async () => {
      await flush()
      harness.socket.open()
      await flush()
    })

    expect((harness.socket.frames[0].body as { deviceId: string }).deviceId).toBe('dev-1')
  })

  it('reports restoring while the reconnect is in flight', async () => {
    localStorage.setItem(StorageKeys.session, JSON.stringify(STORED_SESSION))
    const harness = await renderHookWithProvider(() => useRestoreSession())

    await act(async () => {
      await flush()
    })

    expect(harness.result.current.restoring).toBe(true)
  })

  it('stops restoring once the session is back', async () => {
    localStorage.setItem(StorageKeys.session, JSON.stringify(STORED_SESSION))
    const harness = await renderHookWithProvider(() => useRestoreSession())

    await act(async () => {
      await flush()
      harness.socket.open()
      replyToLast(harness.socket, MsgType.WELCOME, WELCOME)
      await flush()
      replyToLast(harness.socket, MsgType.AUTH_OK, AUTH_OK)
      await flush()
    })

    expect(harness.result.current.restoring).toBe(false)
  })

  /**
   * AUTH_OK carries the profile, so a name or avatar changed on another device
   * while this browser was closed is picked up here. Without it the stored copy
   * would only ever be as fresh as the last full login.
   */
  it('refreshes the profile from the reconnect', async () => {
    localStorage.setItem(StorageKeys.session, JSON.stringify(STORED_SESSION))
    const harness = await renderHookWithProvider(() => useRestoreSession())

    await act(async () => {
      await flush()
      harness.socket.open()
      replyToLast(harness.socket, MsgType.WELCOME, WELCOME)
      await flush()
      replyToLast(harness.socket, MsgType.AUTH_OK, {
        ...AUTH_OK,
        displayName: 'Alice Renamed',
        avatarRef: 'new-avatar',
      })
      await flush()
    })

    expect(useSessionStore.getState().session).toMatchObject({
      displayName: 'Alice Renamed',
      avatarRef: 'new-avatar',
    })
  })

  it('leaves the stored profile alone when the gateway sends none', async () => {
    localStorage.setItem(StorageKeys.session, JSON.stringify(STORED_SESSION))
    const harness = await renderHookWithProvider(() => useRestoreSession())

    await act(async () => {
      await flush()
      harness.socket.open()
      replyToLast(harness.socket, MsgType.WELCOME, WELCOME)
      await flush()
      replyToLast(harness.socket, MsgType.AUTH_OK, { ...AUTH_OK, username: '' })
      await flush()
    })

    expect(useSessionStore.getState().session?.username).toBe('alice')
  })

  it('keeps the token across a successful restore', async () => {
    localStorage.setItem(StorageKeys.session, JSON.stringify(STORED_SESSION))
    const harness = await renderHookWithProvider(() => useRestoreSession())

    await act(async () => {
      await flush()
      harness.socket.open()
      replyToLast(harness.socket, MsgType.WELCOME, WELCOME)
      await flush()
      replyToLast(harness.socket, MsgType.AUTH_OK, AUTH_OK)
      await flush()
    })

    expect(useSessionStore.getState().session?.token).toBeTruthy()
  })

  it('loads the account`s chats before the connection is even up', async () => {
    // The chat list renders from the local registry, so it can be on screen
    // while the socket is still negotiating.
    localStorage.setItem(StorageKeys.session, JSON.stringify(STORED_SESSION))
    await renderHookWithProvider(() => useRestoreSession())

    await act(async () => {
      await flush()
    })

    expect(useChatStore.getState().ownerId).toBe('u1')
  })

  /**
   * A rejected token means the session is gone — expired, or revoked from
   * another device. Retrying cannot help, so the stored session is dropped and
   * the user lands on the login screen rather than on a dead app.
   */
  it('drops the session when the token is refused', async () => {
    localStorage.setItem(StorageKeys.session, JSON.stringify(STORED_SESSION))
    const harness = await renderHookWithProvider(() => useRestoreSession())

    await act(async () => {
      await flush()
      harness.socket.open()
      replyToLast(harness.socket, MsgType.WELCOME, WELCOME)
      await flush()
      replyToLast(harness.socket, MsgType.AUTH_ERR, {
        code: ErrorCode.BAD_TOKEN,
        message: 'expired',
      })
      await flush()
    })

    expect(useSessionStore.getState().session).toBeNull()
    expect(useSessionStore.getState().status).toBe('anonymous')
  })

  it('removes the refused token from storage', async () => {
    // A dead bearer token left on disk is exactly what an XSS on a shared
    // machine would pick up, and it buys nothing.
    localStorage.setItem(StorageKeys.session, JSON.stringify(STORED_SESSION))
    const harness = await renderHookWithProvider(() => useRestoreSession())

    await act(async () => {
      await flush()
      harness.socket.open()
      replyToLast(harness.socket, MsgType.WELCOME, WELCOME)
      await flush()
      replyToLast(harness.socket, MsgType.AUTH_ERR, { code: ErrorCode.SESSION_REVOKED })
      await flush()
    })

    expect(localStorage.getItem(StorageKeys.session)).toBeNull()
  })

  /**
   * A transient failure is different: the server was restarting, or the network
   * was not up yet. The client deliberately does not retry a *first* connect on
   * its own — it rejects so the caller can report — and it clears its reconnect
   * flag when it does. Without a retry here the user would sit on a fully
   * rendered app with a dead socket until they reloaded the page.
   */
  it('keeps the session and retries after a transient failure', async () => {
    vi.useFakeTimers()
    localStorage.setItem(StorageKeys.session, JSON.stringify(STORED_SESSION))
    const harness = await renderHookWithProvider(() => useRestoreSession())

    await act(async () => {
      await flush()
      harness.socket.serverClose()
      await flush()
    })

    expect(useSessionStore.getState().session).not.toBeNull()

    const dialled = FakeSocket.instances.length
    await act(async () => {
      // The retry is scheduled from a rejected promise, so the timer is only
      // armed after the rejection has settled — flush first, then advance.
      await flush()
      await vi.advanceTimersByTimeAsync(5000)
      await flush()
    })

    expect(FakeSocket.instances.length).toBeGreaterThan(dialled)
  })

  it('backs off further with each failed attempt', async () => {
    // A tight redial loop against a gateway that is restarting stops it coming
    // back up at all.
    vi.useFakeTimers()
    localStorage.setItem(StorageKeys.session, JSON.stringify(STORED_SESSION))
    const harness = await renderHookWithProvider(() => useRestoreSession())

    await act(async () => {
      await flush()
      harness.socket.serverClose()
      await flush()
      await vi.advanceTimersByTimeAsync(2000)
      await flush()
    })

    const afterFirst = FakeSocket.instances.length
    await act(async () => {
      FakeSocket.last.serverClose()
      await flush()
      await vi.advanceTimersByTimeAsync(500)
      await flush()
    })

    expect(FakeSocket.instances.length).toBe(afterFirst)
  })

  it('dials once despite Strict Mode`s double mount', async () => {
    // Two dials would race each other and leave one socket orphaned.
    localStorage.setItem(StorageKeys.session, JSON.stringify(STORED_SESSION))
    const harness = await renderHookWithProvider(() => useRestoreSession())

    await act(async () => {
      await flush()
    })
    harness.rerender()
    await act(async () => {
      await flush()
    })

    expect(FakeSocket.instances).toHaveLength(1)
  })

  it('cancels its retry timer on unmount', async () => {
    // A timer that outlives the tree would redial into a provider that is gone.
    vi.useFakeTimers()
    localStorage.setItem(StorageKeys.session, JSON.stringify(STORED_SESSION))
    const harness = await renderHookWithProvider(() => useRestoreSession())

    await act(async () => {
      await flush()
      harness.socket.serverClose()
      await flush()
    })

    const dialled = FakeSocket.instances.length
    harness.unmount()
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000)
    })

    expect(FakeSocket.instances.length).toBe(dialled)
  })
})

describe('useLogout', () => {
  async function loggedIn() {
    const harness = await renderHookWithProvider(() => ({
      logout: useLogout(),
      queryClient: undefined,
    }))
    useSessionStore.getState().setSession(STORED_SESSION)
    useChatStore.getState().load('u1')
    useChatStore.getState().upsert({ id: 'c1', kind: 'group', title: 'Team' })
    useUserDirectory.getState().upsert({ userId: 'u2', name: 'Bob' })
    return harness
  }

  it('clears the session', async () => {
    const harness = await loggedIn()

    act(() => harness.result.current.logout())

    expect(useSessionStore.getState().session).toBeNull()
    expect(useSessionStore.getState().status).toBe('anonymous')
  })

  it('removes the token from storage', async () => {
    const harness = await loggedIn()

    act(() => harness.result.current.logout())

    expect(localStorage.getItem(StorageKeys.session)).toBeNull()
  })

  it('closes the connection', async () => {
    const harness = await loggedIn()
    await harness.connect()

    act(() => harness.result.current.logout())

    expect(harness.client.state).toBe('closed')
  })

  /**
   * Everything below belongs to the account that just left. Leaving any of it
   * behind would show one user's chats, contacts or message history to whoever
   * logs in next on the same browser.
   */
  it('resets the chat registry', async () => {
    const harness = await loggedIn()

    act(() => harness.result.current.logout())

    expect(useChatStore.getState().chats).toEqual({})
    expect(useChatStore.getState().ownerId).toBeNull()
  })

  it('clears the user directory', async () => {
    const harness = await loggedIn()

    act(() => harness.result.current.logout())

    expect(useUserDirectory.getState().users).toEqual({})
  })

  it('clears cached message history', async () => {
    const harness = await loggedIn()
    harness.queryClient.setQueryData(queryKeys.history('c1'), {
      pages: [{ messages: [], nextBefore: 0, done: false }],
      pageParams: [0],
    })

    act(() => harness.result.current.logout())

    expect(harness.queryClient.getQueryData(queryKeys.history('c1'))).toBeUndefined()
  })

  it('is safe to call twice', async () => {
    const harness = await loggedIn()

    act(() => harness.result.current.logout())
    expect(() => act(() => harness.result.current.logout())).not.toThrow()
  })
})

describe('useSessionExpiryWatcher', () => {
  /**
   * A session revoked from another device arrives out of the blue, with no
   * request of ours to reject. Without this the app would keep rendering a
   * logged-in shell over a connection the gateway has already disowned.
   */
  it('logs out when the gateway reports the session gone', async () => {
    const harness = await renderHookWithProvider(() => useSessionExpiryWatcher())
    await harness.connect()
    useSessionStore.getState().setSession(STORED_SESSION)

    await act(async () => {
      harness.socket.deliver(MsgType.ERROR, {
        code: ErrorCode.SESSION_REVOKED,
        message: 'revoked elsewhere',
      })
      await flush()
    })

    expect(useSessionStore.getState().session).toBeNull()
  })

  it('ignores an ordinary error', async () => {
    // Only the auth class means the session is gone; logging out on a rate limit
    // would throw the user off the app for a transient condition.
    const harness = await renderHookWithProvider(() => useSessionExpiryWatcher())
    await harness.connect()
    useSessionStore.getState().setSession(STORED_SESSION)

    await act(async () => {
      harness.socket.deliver(MsgType.ERROR, {
        code: ErrorCode.RATE_LIMITED,
        message: 'slow down',
      })
      await flush()
    })

    expect(useSessionStore.getState().session).not.toBeNull()
  })

  it('unsubscribes on unmount', async () => {
    const harness = await renderHookWithProvider(() => useSessionExpiryWatcher())
    await harness.connect()
    useSessionStore.getState().setSession(STORED_SESSION)

    harness.unmount()

    await act(async () => {
      harness.socket.deliver(MsgType.ERROR, { code: ErrorCode.SESSION_REVOKED })
      await flush()
    })

    expect(useSessionStore.getState().session).not.toBeNull()
  })
})
