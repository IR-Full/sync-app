'use client'

import Link from 'next/link'
import { useEffect, useState } from 'react'

import { useChatStore } from '@/entities/chat'
import { labelForUser, useUserDirectory } from '@/entities/user'
import { useTranslate } from '@/shared/i18n'
import { EmptyState, Modal, Spinner, TextField } from '@/shared/ui'

import { useMessageSearch } from '../model/use-search'

/**
 * Full-text search across every chat the user belongs to.
 *
 * Server-side and permission-filtered, so it finds messages that were never
 * loaded into this browser — which is exactly what the local chat-list filter
 * cannot do. Debounced because SEARCH is rate-limited per connection.
 */
export function SearchPanel({ open, onClose }: { open: boolean; onClose: () => void }) {
  const t = useTranslate()
  const [input, setInput] = useState('')
  const [query, setQuery] = useState('')
  const chats = useChatStore((state) => state.chats)
  const directory = useUserDirectory((state) => state.users)

  useEffect(() => {
    const timer = setTimeout(() => setQuery(input), 350)
    return () => clearTimeout(timer)
  }, [input])

  const { data: hits, isFetching } = useMessageSearch(query)

  return (
    <Modal open={open} onClose={onClose} title={t('search.title')}>
      <TextField
        placeholder={t('search.placeholder')}
        value={input}
        autoFocus
        onChange={(event) => setInput(event.target.value)}
      />

      <div className="mt-3 max-h-96 overflow-y-auto">
        {input.trim().length < 2 ? (
          <p className="py-6 text-center text-sm text-ink-faint">{t('search.hint')}</p>
        ) : isFetching ? (
          <div className="flex justify-center py-6">
            <Spinner />
          </div>
        ) : !hits?.length ? (
          <EmptyState title={t('search.empty')} />
        ) : (
          <ul className="flex flex-col gap-1">
            {hits.map((hit) => (
              <li key={hit.messageId}>
                <Link
                  href={`/chats/${hit.chatId}`}
                  onClick={onClose}
                  className="block rounded-xl px-3 py-2 transition-colors hover:bg-surface-hover"
                >
                  <span className="flex items-baseline justify-between gap-2">
                    <span className="truncate text-xs font-medium text-accent">
                      {chats[hit.chatId]?.title ?? `#${hit.chatId.slice(-6)}`}
                    </span>
                    <span className="shrink-0 text-[11px] text-ink-faint">
                      {labelForUser(directory[hit.senderId], hit.senderId)}
                    </span>
                  </span>
                  <span className="mt-0.5 block truncate text-sm text-ink">{hit.text}</span>
                </Link>
              </li>
            ))}
          </ul>
        )}
      </div>
    </Modal>
  )
}
