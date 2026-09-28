import { waitFor } from '@testing-library/react'
import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useDraftStore } from '@/entities/draft'
import { MsgType } from '@/shared/api'
import {
  flush,
  installFakeSocket,
  renderHookWithProvider,
  type Harness,
} from '@/shared/api/syncapp/test-harness'
import { useDraftSync, useDraftWriter } from './use-drafts'

beforeEach(() => {
  installFakeSocket()
  localStorage.clear()
  vi.spyOn(console, 'error').mockImplementation(() => {})
  useDraftStore.getState().clear()
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  vi.useRealTimers()
})

const draftFrames = (harness: Harness<unknown>) =>
  harness.socket.frames.filter((frame) => frame.type === MsgType.DRAFT_SET)

/**
 * Answers the DRAFT_SYNC request specifically.
 *
 * Not `envelopes.at(-1)`: the query fires from an effect, so by the time a test
 * replies the last frame on the wire may be something else entirely — and a
 * reply with the wrong correlation id is simply ignored, which reads as "the
 * hook did nothing".
 */
async function respondToDraftSync<T extends { isSuccess: boolean }>(
  harness: Harness<T>,
  drafts: object[],
) {
  const request = harness.socket.envelopes.find(
    (envelope) => envelope.type === MsgType.DRAFT_SYNC,
  )
  if (!request) throw new Error('the hook never sent a DRAFT_SYNC request')

  await act(async () => {
    harness.socket.deliver(MsgType.DRAFTS, { drafts }, { requestId: request.requestId })
    await flush()
  })
  // The query resolving and the effect that merges it are two separate React
  // commits, so draining microtasks is not enough — wait for the render.
  await waitFor(() => expect(harness.result.current.isSuccess).toBe(true))
}

describe('useDraftSync', () => {
  it('asks for every draft rather than paging from a cursor', async () => {
    /*
     * DRAFT_SYNC is cursor-based, but the set is per-user and tiny. Asking for
     * everything removes the one failure a cursor can have — drifting, and
     * silently hiding a draft the user did write.
     */
    const harness = await renderHookWithProvider(() => useDraftSync())
    await harness.connect()

    const request = harness.socket.frames.find((frame) => frame.type === MsgType.DRAFT_SYNC)
    expect(request?.body).toMatchObject({ since: 0 })
  })

  it('merges the drafts it received into the store', async () => {
    const harness = await renderHookWithProvider(() => useDraftSync())
    await harness.connect()

    await respondToDraftSync(harness, [
      { chatId: 'c1', text: 'unsent', replyTo: 'm7', updatedAt: 2_000 },
    ])

    expect(useDraftStore.getState().byChat.c1).toMatchObject({
      text: 'unsent',
      replyTo: 'm7',
    })
  })

  it('asks for nothing while disconnected', async () => {
    const harness = await renderHookWithProvider(() => useDraftSync())
    await act(async () => {
      await flush()
    })

    expect(harness.result.current.isFetching).toBe(false)
  })

  it('handles an empty draft set', async () => {
    const harness = await renderHookWithProvider(() => useDraftSync())
    await harness.connect()

    await respondToDraftSync(harness, [])

    expect(useDraftStore.getState().byChat).toEqual({})
  })
})

describe('useDraftWriter', () => {
  /**
   * Every keystroke would be a frame the server has to mirror to the user's other
   * devices, and DRAFT_SET counts as state-changing for flood control — so a
   * writer that sent on each character would get the connection throttled while
   * the user was still typing.
   */
  it('waits for a pause before writing', async () => {
    vi.useFakeTimers()
    const harness = await renderHookWithProvider(() => useDraftWriter('c1', 800))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => harness.result.current('h'))
    act(() => harness.result.current('he'))
    act(() => harness.result.current('hello'))

    expect(draftFrames(harness)).toHaveLength(0)

    await act(async () => {
      await vi.advanceTimersByTimeAsync(800)
    })

    expect(draftFrames(harness)).toHaveLength(1)
  })

  it('writes only the last text typed', async () => {
    vi.useFakeTimers()
    const harness = await renderHookWithProvider(() => useDraftWriter('c1', 800))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => harness.result.current('draft one'))
    act(() => harness.result.current('draft two'))

    await act(async () => {
      await vi.advanceTimersByTimeAsync(800)
    })

    expect(draftFrames(harness).at(-1)?.body).toMatchObject({ chatId: 'c1', text: 'draft two' })
  })

  it('mirrors the draft into the local store', async () => {
    // The composer restores from the store, so a write that only went to the
    // server would leave the local view a debounce behind.
    vi.useFakeTimers()
    const harness = await renderHookWithProvider(() => useDraftWriter('c1', 800))
    await harness.connect()

    act(() => harness.result.current('hello'))
    await act(async () => {
      await vi.advanceTimersByTimeAsync(800)
    })

    expect(useDraftStore.getState().byChat.c1?.text).toBe('hello')
  })

  it('does not rewrite the same text twice', async () => {
    // Re-entering a chat replays the restored text through the writer; sending it
    // again spends a frame to tell the server what it already knows.
    vi.useFakeTimers()
    const harness = await renderHookWithProvider(() => useDraftWriter('c1', 800))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => harness.result.current('hello'))
    await act(async () => {
      await vi.advanceTimersByTimeAsync(800)
    })
    act(() => harness.result.current('hello'))
    await act(async () => {
      await vi.advanceTimersByTimeAsync(800)
    })

    expect(draftFrames(harness)).toHaveLength(1)
  })

  /**
   * Switching chats or closing the composer inside the debounce window used to
   * discard whatever was typed last — which is precisely the case a draft exists
   * for. The flush on unmount is the fix, and it is easy to regress back into a
   * bare `clearTimeout`.
   */
  it('flushes a pending draft on unmount instead of losing it', async () => {
    vi.useFakeTimers()
    const harness = await renderHookWithProvider(() => useDraftWriter('c1', 800))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => harness.result.current('typed but not yet saved'))
    act(() => harness.unmount())

    expect(draftFrames(harness)).toHaveLength(1)
    expect(draftFrames(harness)[0].body).toMatchObject({ text: 'typed but not yet saved' })
  })

  it('does not write anything on unmount when nothing was pending', async () => {
    vi.useFakeTimers()
    const harness = await renderHookWithProvider(() => useDraftWriter('c1', 800))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => harness.unmount())

    expect(draftFrames(harness)).toHaveLength(0)
  })

  /**
   * A handle target has no chat yet. Asking the server to resolve one would
   * CREATE the direct chat — so typing into a composer and closing it without
   * sending would leave a conversation behind.
   */
  it('never writes a draft for a handle target', async () => {
    vi.useFakeTimers()
    const harness = await renderHookWithProvider(() => useDraftWriter('@bob', 800))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => harness.result.current('hello'))
    await act(async () => {
      await vi.advanceTimersByTimeAsync(800)
    })

    expect(draftFrames(harness)).toHaveLength(0)
  })

  it('writes nothing without a chat id', async () => {
    vi.useFakeTimers()
    const harness = await renderHookWithProvider(() => useDraftWriter('', 800))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => harness.result.current('hello'))
    await act(async () => {
      await vi.advanceTimersByTimeAsync(800)
    })

    expect(draftFrames(harness)).toHaveLength(0)
  })

  it('writes nothing while disconnected', async () => {
    vi.useFakeTimers()
    const harness = await renderHookWithProvider(() => useDraftWriter('c1', 800))

    expect(() => act(() => harness.result.current('hello'))).not.toThrow()
  })

  it('swallows a send failure rather than surfacing it', async () => {
    // The next pause retries; a dropped draft is not worth an error banner.
    vi.useFakeTimers()
    const harness = await renderHookWithProvider(() => useDraftWriter('c1', 800))
    await harness.connect()
    vi.spyOn(harness.client, 'send').mockImplementation(() => {
      throw new Error('socket went away')
    })

    act(() => harness.result.current('hello'))
    await act(async () => {
      expect(async () => vi.advanceTimersByTimeAsync(800)).not.toThrow()
    })
  })

  it('restarts the debounce on each keystroke', async () => {
    // Otherwise a fast typist would have their draft written mid-word on a fixed
    // schedule rather than when they paused.
    vi.useFakeTimers()
    const harness = await renderHookWithProvider(() => useDraftWriter('c1', 800))
    await harness.connect()
    harness.socket.sent.length = 0

    act(() => harness.result.current('a'))
    await act(async () => {
      await vi.advanceTimersByTimeAsync(600)
    })
    act(() => harness.result.current('ab'))
    await act(async () => {
      await vi.advanceTimersByTimeAsync(600)
    })

    expect(draftFrames(harness)).toHaveLength(0)

    await act(async () => {
      await vi.advanceTimersByTimeAsync(300)
    })

    expect(draftFrames(harness)).toHaveLength(1)
  })
})
