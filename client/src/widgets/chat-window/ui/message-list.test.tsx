import { act, cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { ChatMessage } from '@/entities/message'

import { MessageList } from './message-list'

afterEach(cleanup)

function transcript(count: number): ChatMessage[] {
  return Array.from({ length: count }, (_, i) => ({
    id: `m${i}`,
    chatId: 'c1',
    senderId: 'peer',
    seq: i + 1,
    text: `message ${i}`,
    timestamp: 1_700_000_000_000 + i * 60_000,
    edited: false,
    deleted: false,
    replyTo: '',
    mediaRef: '',
    attachment: null,
    forward: null,
    threadRoot: '',
    replyCount: 0,
    expiresAt: 0,
    outgoing: false,
    status: 'sent' as const,
  }))
}

function renderList(
  messages: ChatMessage[],
  overrides: Partial<Parameters<typeof MessageList>[0]> = {},
) {
  return render(
    <MessageList
      messages={messages}
      showSenders={false}
      senderLabel={() => 'Peer'}
      peerReadSeq={0}
      hasOlder={false}
      loadingOlder={false}
      onLoadOlder={() => {}}
      onVisibleSeq={() => {}}
      emptyLabel="empty"
      {...overrides}
    />,
  )
}

/** The rendered message bubbles, in document order. */
const bubbleTexts = () =>
  screen.queryAllByText(/^message \d+$/).map((node) => node.textContent ?? '')

describe('mounted window', () => {
  /**
   * The cost this bounds: every message in the cache used to become a DOM
   * subtree the instant a chat was opened — bubble, reactions, avatar, action
   * menu — including the thousands nobody was going to scroll to.
   */
  it('mounts only the tail of a long transcript', () => {
    renderList(transcript(500))
    const shown = bubbleTexts()

    expect(shown.length).toBeLessThan(500)
    expect(shown.length).toBeGreaterThan(0)
  })

  /**
   * Slicing from the END is the whole safety argument: the newest message is
   * always mounted, so an arriving one is never hidden behind a window.
   */
  it('always keeps the newest message mounted', () => {
    renderList(transcript(500))
    expect(bubbleTexts().at(-1)).toBe('message 499')
  })

  it('mounts a short transcript in full', () => {
    renderList(transcript(5))
    expect(bubbleTexts()).toHaveLength(5)
  })

  it('renders the empty label rather than an empty scroller', () => {
    renderList([])
    expect(screen.getByText('empty')).toBeTruthy()
  })

  /**
   * A message arriving after mount must widen the window rather than push an
   * older row out of it. Unmounting from the top would shift everything below
   * upward while someone is reading — the same jolt the prepend anchoring
   * exists to prevent, arriving from the other end.
   */
  it('never unmounts a row when new messages arrive', () => {
    const messages = transcript(500)
    const { rerender } = renderList(messages)
    const before = bubbleTexts()

    const arrived: ChatMessage[] = [
      ...messages,
      { ...messages[0], id: 'new1', seq: 501, text: 'message 500' },
    ]
    act(() => {
      rerender(
        <MessageList
          messages={arrived}
          showSenders={false}
          senderLabel={() => 'Peer'}
          peerReadSeq={0}
          hasOlder={false}
          loadingOlder={false}
          onLoadOlder={() => {}}
          onVisibleSeq={() => {}}
          emptyLabel="empty"
        />,
      )
    })

    const after = bubbleTexts()
    // Everything that was on screen is still on screen, plus the new one.
    for (const text of before) expect(after).toContain(text)
    expect(after.at(-1)).toBe('message 500')
  })
})

describe('reaching the top', () => {
  /**
   * Two different places "more" can come from, and the free one has to be
   * exhausted first — otherwise a chat with a thousand cached messages fetches
   * a network page while nine hundred it already holds sit unmounted.
   */
  it('grows the window before asking the network for an older page', () => {
    const onLoadOlder = vi.fn()
    const { container } = renderList(transcript(500), { hasOlder: true, onLoadOlder })
    const scroller = container.querySelector('[role="log"]') as HTMLDivElement

    const mountedBefore = bubbleTexts().length
    act(() => {
      scroller.scrollTop = 0
      scroller.dispatchEvent(new Event('scroll', { bubbles: true }))
    })

    expect(bubbleTexts().length).toBeGreaterThan(mountedBefore)
    expect(onLoadOlder).not.toHaveBeenCalled()
  })

  it('asks the network once the whole transcript is mounted', () => {
    const onLoadOlder = vi.fn()
    const { container } = renderList(transcript(10), { hasOlder: true, onLoadOlder })
    const scroller = container.querySelector('[role="log"]') as HTMLDivElement

    act(() => {
      scroller.scrollTop = 0
      scroller.dispatchEvent(new Event('scroll', { bubbles: true }))
    })

    expect(onLoadOlder).toHaveBeenCalled()
  })

  /**
   * "Beginning of history" must not appear while messages are merely unmounted:
   * it would claim the conversation starts where the window happens to end.
   */
  it('does not claim the start of history while rows are still unmounted', () => {
    renderList(transcript(500), { hasOlder: false })
    expect(screen.queryByText(/beginning|начало/i)).toBeNull()
  })
})
