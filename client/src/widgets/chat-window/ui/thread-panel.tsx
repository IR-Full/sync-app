'use client'

import { useMemo, useState } from 'react'

import { MessageBubble, type ChatMessage } from '@/entities/message'
import { useThread } from '@/features/threads'
import { useTranslate } from '@/shared/i18n'
import { Button, Modal, Spinner, TextField } from '@/shared/ui'

/**
 * The reply branch under a message.
 *
 * Threads page forward (oldest first) unlike the main transcript, because a
 * thread is read from its start. Replying is an ordinary send with `replyTo`
 * pointing at the root — the server resolves the thread root at write time.
 */
export function ThreadPanel({
  chatId,
  root,
  senderLabel,
  onClose,
  onReply,
}: {
  chatId: string
  root: ChatMessage
  senderLabel: (userId: string) => string
  onClose: () => void
  onReply: (text: string) => void
}) {
  const t = useTranslate()
  const thread = useThread(chatId, root.id)
  const [text, setText] = useState('')

  const replies = useMemo(
    () => (thread.data?.pages ?? []).flatMap((page) => page.messages),
    [thread.data],
  )

  return (
    <Modal open onClose={onClose} title={t('thread.title')}>
      <div className="flex max-h-[60vh] flex-col gap-2 overflow-y-auto">
        <MessageBubble message={root} senderLabel={senderLabel(root.senderId)} showSender />

        <div className="border-line border-t pt-2">
          {thread.isLoading ? (
            <div className="flex justify-center py-4">
              <Spinner className="size-4" />
            </div>
          ) : replies.length === 0 ? (
            <p className="text-ink-faint py-4 text-center text-sm">{t('thread.empty')}</p>
          ) : (
            <div className="flex flex-col gap-1">
              {replies.map((reply) => (
                <MessageBubble
                  key={reply.id}
                  message={reply}
                  senderLabel={senderLabel(reply.senderId)}
                  showSender={!reply.outgoing}
                />
              ))}
            </div>
          )}

          {thread.hasNextPage && (
            <div className="mt-2 flex justify-center">
              <Button
                size="small"
                variant="secondary"
                loading={thread.isFetchingNextPage}
                onClick={() => thread.fetchNextPage()}
              >
                {t('common.loading')}
              </Button>
            </div>
          )}
        </div>
      </div>

      <div className="mt-3 flex items-end gap-2">
        <TextField
          placeholder={t('chat.placeholder')}
          value={text}
          onChange={(event) => setText(event.target.value)}
          className="flex-1"
        />
        <Button
          disabled={!text.trim()}
          onClick={() => {
            onReply(text.trim())
            setText('')
          }}
        >
          {t('chat.send')}
        </Button>
      </div>
    </Modal>
  )
}
