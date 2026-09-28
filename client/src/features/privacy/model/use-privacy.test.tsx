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
import { usePrivacy, useSetPrivacy } from './use-privacy'

const SELF = {
  userId: 'self',
  username: 'alice',
  deviceId: 'dev-1',
  sessionId: 's1',
  token: 'tok-1',
  resumeToken: 'resume-1',
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

describe('usePrivacy', () => {
  it('reads the three settings', async () => {
    const harness = await renderHookWithProvider(() => usePrivacy())
    await harness.connect()
    await act(async () => {
      replyToLast(harness.socket, MsgType.PRIVACY, {
        lastSeen: 'contacts',
        avatar: 'nobody',
        groups: 'everyone',
      })
    })

    await waitFor(() => expect(harness.result.current.data).toBeDefined())
    expect(harness.result.current.data).toEqual({
      lastSeen: 'contacts',
      avatar: 'nobody',
      groups: 'everyone',
    })
  })

  /**
   * An unfamiliar value reads as the STRICTEST setting, not the loosest.
   *
   * If a newer server grows a narrower option, guessing "everyone" would draw a
   * screen claiming the account is public while the server keeps it private —
   * and saving that screen would quietly widen the user's own visibility.
   */
  it('treats an unrecognised value as the strictest setting', async () => {
    const harness = await renderHookWithProvider(() => usePrivacy())
    await harness.connect()
    await act(async () => {
      replyToLast(harness.socket, MsgType.PRIVACY, {
        lastSeen: 'mutuals-only',
        avatar: 'everyone',
        groups: 'everyone',
      })
    })

    await waitFor(() => expect(harness.result.current.data).toBeDefined())
    expect(harness.result.current.data!.lastSeen).toBe('nobody')
  })
})

describe('useSetPrivacy', () => {
  /**
   * All three fields go every time, because the protocol requires it: a partial
   * update cannot distinguish "nobody" from "not specified".
   */
  it('sends every field, not just the changed one', async () => {
    const harness = await renderHookWithProvider(() => useSetPrivacy())
    await harness.connect()

    act(() =>
      harness.result.current.mutate({
        lastSeen: 'nobody',
        avatar: 'contacts',
        groups: 'everyone',
      }),
    )
    await act(async () => {
      await flush()
      replyToLast(harness.socket, MsgType.PRIVACY, {
        lastSeen: 'nobody',
        avatar: 'contacts',
        groups: 'everyone',
      })
    })

    const frame = harness.socket.frames.find((f) => f.type === MsgType.PRIVACY_SET)
    expect(frame?.body).toMatchObject({
      lastSeen: 'nobody',
      avatar: 'contacts',
      groups: 'everyone',
    })
  })

  it('surfaces a rejection rather than pretending it saved', async () => {
    const harness = await renderHookWithProvider(() => useSetPrivacy())
    await harness.connect()

    act(() =>
      harness.result.current.mutate({
        lastSeen: 'everyone',
        avatar: 'everyone',
        groups: 'everyone',
      }),
    )
    await act(async () => {
      await flush()
      replyToLast(harness.socket, MsgType.ERROR, { code: 3002, message: 'bad privacy' })
    })

    await waitFor(() => expect(harness.result.current.isError).toBe(true))
  })
})
