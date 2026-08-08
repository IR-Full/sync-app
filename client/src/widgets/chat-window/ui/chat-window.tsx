'use client'

import { useRouter } from 'next/navigation'
import { useCallback, useMemo, useState } from 'react'

import { unreadCount, useChatStore } from '@/entities/chat'
import { useDraft } from '@/entities/draft'
import { flattenHistory, type ChatMessage } from '@/entities/message'
import { usePollStore } from '@/entities/poll'
import { useSessionStore } from '@/entities/session'
import { labelForUser, useUserDirectory } from '@/entities/user'
import { useCallActions } from '@/features/calls'
import { ChatAdminPanel } from '@/features/chat-admin'
import { useDraftWriter } from '@/features/drafts'
import { AttachmentView, useMediaUpload } from '@/features/media'
import { MessageMenu, useForwardMessage, useMessageActions } from '@/features/message-actions'
import { useChatHistory } from '@/features/message-history'
import { useChatPins, useTogglePin } from '@/features/pins'
import { PollCard, PollComposer, usePollActions } from '@/features/polls'
import { useMarkRead, usePeerReadSeq } from '@/features/read-receipts'
import { useScheduleMessage } from '@/features/scheduled'
import { SearchPanel } from '@/features/search'
import { SecretChatPanel } from '@/features/secret-chats'
import { isHandleTarget, MessageComposer, useSendMessage } from '@/features/send-message'
import { useTypingNotifier, useTypingUsers } from '@/features/typing-indicator'
import { useIsConnected } from '@/shared/api'
import { useTranslate } from '@/shared/i18n'
import { Avatar, Spinner } from '@/shared/ui'

import { MessageList } from './message-list'
import { ThreadPanel } from './thread-panel'

/**
 * A single conversation.
 *
 * `target` is either a real chat id or a "@handle" for a direct chat that does
 * not exist yet. The distinction matters beyond cosmetics: addressing a handle
 * makes the gateway *create* the chat, so a handle target must not trigger a
 * history fetch — that would silently create an empty conversation just because
 * someone opened a compose screen.
 */
export function ChatWindow({ target }: { target: string }) {
  const t = useTranslate()
  const router = useRouter()
  const connected = useIsConnected()

  const isNew = isHandleTarget(target)
  const chatId = isNew ? '' : target
  const chat = useChatStore((state) => state.chats[target])
  const selfId = useSessionStore((state) => state.session?.userId ?? '')
  const directory = useUserDirectory((state) => state.users)
  const pollsByMessage = usePollStore((state) => state.byMessage)
  const pollsById = usePollStore((state) => state.byId)

  // Keyed on the target even when it is a handle, so optimistic messages have a
  // cache entry to live in; fetching stays off until the chat actually exists.
  const history = useChatHistory(target, !isNew)
  const typingUsers = useTypingUsers(target)
  const notifyTyping = useTypingNotifier(chatId)
  const peerReadSeq = usePeerReadSeq(target)
  const markRead = useMarkRead(chatId)
  const draft = useDraft(target)
  const writeDraft = useDraftWriter(chatId)
  const { upload } = useMediaUpload()
  const pins = useChatPins(chatId)
  const togglePin = useTogglePin(chatId)
  const { edit, remove, react } = useMessageActions(chatId)
  const forwardMessage = useForwardMessage()
  const { vote, close: closePoll } = usePollActions(chatId)
  const scheduleMessage = useScheduleMessage(chatId)
  const { startCall } = useCallActions()

  const [replyTo, setReplyTo] = useState<{ id: string; label: string } | null>(null)
  const [editing, setEditing] = useState<ChatMessage | null>(null)
  const [thread, setThread] = useState<ChatMessage | null>(null)
  const [searchOpen, setSearchOpen] = useState(false)
  const [adminOpen, setAdminOpen] = useState(false)
  const [pollOpen, setPollOpen] = useState(false)
  const [secretOpen, setSecretOpen] = useState(false)

  const { send, retry } = useSendMessage(target, (resolved) => {
    // The first message to a handle resolved into a real chat — switch the URL
    // to it so a reload lands on the conversation, not the compose screen.
    router.replace(`/chats/${resolved}`)
  })

  const messages: ChatMessage[] = useMemo(
    () => flattenHistory(history.data as never),
    [history.data],
  )

  const senderLabel = useCallback(
    (userId: string) => labelForUser(directory[userId], userId),
    [directory],
  )

  const pinnedIds = useMemo(
    () => new Set((pins.data ?? []).map((pin) => pin.messageId)),
    [pins.data],
  )

  const title = chat?.title ?? (isNew ? target : (chat?.id ?? target))
  const isGroup = chat ? chat.kind !== 'direct' : false
  const peer = chat?.peerUserId ? directory[chat.peerUserId] : undefined

  const subtitle = typingUsers.length
    ? typingUsers.length > 1
      ? t('chat.typingMany')
      : t('chat.typing', { name: senderLabel(typingUsers[0]) })
    : isGroup
      ? undefined
      : peer?.online
        ? t('chat.online')
        : undefined

  const pinnedPreview = useMemo(() => {
    if (!pins.data?.length) return null
    const newest = pins.data[pins.data.length - 1]
    return messages.find((message) => message.id === newest.messageId) ?? null
  }, [pins.data, messages])

  return (
    <section className="bg-surface flex h-full min-w-0 flex-1 flex-col">
      <header className="border-line flex items-center gap-3 border-b px-4 py-2.5">
        <button
          type="button"
          onClick={() => router.push('/chats')}
          aria-label={t('nav.back')}
          className="text-ink-muted hover:bg-surface-hover -ml-1 rounded-lg p-1.5 md:hidden"
        >
          <svg
            viewBox="0 0 20 20"
            className="size-5"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.8"
          >
            <path d="M12 4l-6 6 6 6" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        </button>

        <Avatar seed={chat?.id ?? target} name={title} online={peer?.online} />
        <div className="min-w-0 flex-1">
          <h1 className="text-ink truncate text-sm font-semibold">{title}</h1>
          {subtitle && (
            <p className="text-accent truncate text-xs" aria-live="polite">
              {subtitle}
            </p>
          )}
        </div>

        {chat && unreadCount(chat) > 0 && (
          <span className="text-ink-faint text-xs">{unreadCount(chat)}</span>
        )}

        {!isNew && (
          <>
            <button
              type="button"
              onClick={() => void startCall(chatId, 'audio')}
              aria-label={t('call.audio')}
              title={t('call.audio')}
              className="hover:bg-surface-hover text-ink-muted hover:text-ink rounded-lg p-1.5 transition-colors"
            >
              <svg
                viewBox="0 0 20 20"
                className="size-5"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.6"
              >
                <path
                  d="M6.5 3.5l1.8 3-1.4 1.6a9 9 0 004 4l1.6-1.4 3 1.8-.6 2.4c-.2.6-.8 1-1.4.9C8.4 15 5 11.6 4.2 6.5c-.1-.6.3-1.2.9-1.4l1.4-.4z"
                  strokeLinejoin="round"
                />
              </svg>
            </button>
            <button
              type="button"
              onClick={() => void startCall(chatId, 'video')}
              aria-label={t('call.video')}
              title={t('call.video')}
              className="hover:bg-surface-hover text-ink-muted hover:text-ink rounded-lg p-1.5 transition-colors"
            >
              <svg
                viewBox="0 0 20 20"
                className="size-5"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.6"
              >
                <rect x="2.5" y="5.5" width="10" height="9" rx="2" />
                <path d="M12.5 9l5-2.5v7L12.5 11" strokeLinejoin="round" />
              </svg>
            </button>
          </>
        )}

        <button
          type="button"
          onClick={() => setSearchOpen(true)}
          aria-label={t('search.open')}
          title={t('search.open')}
          className="text-ink-muted hover:bg-surface-hover hover:text-ink rounded-lg p-1.5 transition-colors"
        >
          <svg
            viewBox="0 0 20 20"
            className="size-5"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.7"
          >
            <circle cx="9" cy="9" r="5.5" />
            <path d="M13.5 13.5L17 17" strokeLinecap="round" />
          </svg>
        </button>

        {!isNew && chat?.peerUserId && (
          <button
            type="button"
            onClick={() => setSecretOpen(true)}
            aria-label={t('secret.open')}
            title={t('secret.open')}
            className="hover:bg-surface-hover text-ink-muted hover:text-ink rounded-lg p-1.5 transition-colors"
          >
            <svg
              viewBox="0 0 20 20"
              className="size-5"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.6"
            >
              <rect x="4" y="9" width="12" height="8" rx="2" />
              <path d="M7 9V6.5a3 3 0 016 0V9" strokeLinecap="round" />
            </svg>
          </button>
        )}

        {!isNew && (
          <button
            type="button"
            onClick={() => setAdminOpen(true)}
            aria-label={t('admin.title')}
            title={t('admin.title')}
            className="text-ink-muted hover:bg-surface-hover hover:text-ink rounded-lg p-1.5 transition-colors"
          >
            <svg
              viewBox="0 0 20 20"
              className="size-5"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.6"
            >
              <circle cx="10" cy="10" r="7" />
              <path d="M10 7v3.5l2.4 1.4" strokeLinecap="round" />
            </svg>
          </button>
        )}
      </header>

      {pinnedPreview && (
        <div className="border-line bg-surface-sunken flex items-center gap-2 border-b px-4 py-1.5 text-xs">
          <span className="text-accent font-medium">{t('chat.pinnedBar')}</span>
          <span className="text-ink-muted min-w-0 flex-1 truncate">
            {pinnedPreview.text || pinnedPreview.attachment?.filename || '—'}
          </span>
        </div>
      )}

      {history.isLoading && !isNew ? (
        <div className="flex flex-1 items-center justify-center">
          <Spinner />
        </div>
      ) : history.isError && !isNew ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-2 text-sm">
          <p className="text-danger">{t('error.title')}</p>
          <button
            type="button"
            onClick={() => history.refetch()}
            className="text-accent underline-offset-4 hover:underline"
          >
            {t('error.retry')}
          </button>
        </div>
      ) : (
        <MessageList
          messages={messages}
          showSenders={isGroup}
          senderLabel={senderLabel}
          peerReadSeq={peerReadSeq}
          hasOlder={!isNew && (history.hasNextPage ?? false)}
          loadingOlder={history.isFetchingNextPage}
          onLoadOlder={() => history.fetchNextPage()}
          onVisibleSeq={markRead}
          onRetry={retry}
          onToggleReaction={(message, emoji) => void react(message, emoji)}
          isPinned={(messageId) => pinnedIds.has(messageId)}
          onOpenThread={setThread}
          renderExtras={(message) => {
            const pollId = pollsByMessage[message.id]
            const poll = pollId ? pollsById[pollId] : undefined
            if (poll) {
              return (
                <PollCard
                  poll={poll}
                  canClose={message.senderId === selfId && !poll.closed}
                  onVote={(option) => void vote(poll.pollId, option)}
                  onClose={() => void closePoll(poll.pollId)}
                />
              )
            }
            if (message.attachment) {
              return <AttachmentView attachment={message.attachment} own={message.outgoing} />
            }
            return null
          }}
          renderActions={(message) => (
            <MessageMenu
              message={message}
              canEdit={message.senderId === selfId}
              canDelete={message.senderId === selfId}
              pinned={pinnedIds.has(message.id)}
              onReact={(emoji) => void react(message, emoji)}
              onEdit={() => setEditing(message)}
              onDelete={() => remove(message)}
              onReply={() =>
                setReplyTo({ id: message.id, label: senderLabel(message.senderId) })
              }
              onForward={() => {
                const to = window.prompt(t('chat.forward'), '@')
                if (to?.trim()) void forwardMessage(message, to.trim())
              }}
              onTogglePin={() =>
                togglePin.mutate({ messageId: message.id, pinned: pinnedIds.has(message.id) })
              }
              onOpenThread={() => setThread(message)}
            />
          )}
          emptyLabel={isNew ? t('compose.directHint') : t('chat.empty')}
        />
      )}

      <MessageComposer
        // Remounting is how the composer picks up a different chat's draft or
        // an edit target, instead of syncing props into state with an effect.
        key={`${target}:${editing?.id ?? ''}`}
        onSend={send}
        onTyping={notifyTyping}
        onDraftChange={writeDraft}
        onUpload={isNew ? undefined : async (file) => (await upload(file)).attachment}
        onOpenPoll={isNew ? undefined : () => setPollOpen(true)}
        onSchedule={
          isNew ? undefined : (text, sendAt) => scheduleMessage.mutate({ text, sendAt })
        }
        initialText={editing ? editing.text : draft}
        replyTo={replyTo}
        onCancelReply={() => setReplyTo(null)}
        editing={editing ? { id: editing.id, text: editing.text } : null}
        onSubmitEdit={(text) => {
          if (editing) edit(editing, text)
          setEditing(null)
        }}
        onCancelEdit={() => setEditing(null)}
        // Composing offline is allowed on purpose — the message queues and the
        // outbox delivers it on reconnect.
        disabled={!connected && !selfId}
      />

      <SearchPanel open={searchOpen} onClose={() => setSearchOpen(false)} />
      {!isNew && (
        <>
          <ChatAdminPanel
            chatId={chatId}
            open={adminOpen}
            onClose={() => setAdminOpen(false)}
          />
          <PollComposer chatId={chatId} open={pollOpen} onClose={() => setPollOpen(false)} />
          {chat?.peerUserId && (
            <SecretChatPanel
              peerUserId={chat.peerUserId}
              peerLabel={title}
              open={secretOpen}
              onClose={() => setSecretOpen(false)}
            />
          )}
          {thread && (
            <ThreadPanel
              chatId={chatId}
              root={thread}
              senderLabel={senderLabel}
              onClose={() => setThread(null)}
              onReply={(text) => send(text, { replyTo: thread.id })}
            />
          )}
        </>
      )}
    </section>
  )
}
