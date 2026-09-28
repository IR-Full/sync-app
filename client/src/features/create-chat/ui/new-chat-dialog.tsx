'use client'

import { useRouter } from 'next/navigation'
import { useState } from 'react'

import { ErrorCode, ProtocolError } from '@/shared/api'
import { useTranslate } from '@/shared/i18n'
import { cn } from '@/shared/lib/cn'
import { Button, ErrorNote, Modal, TextField } from '@/shared/ui'

import {
  directChatTarget,
  useCreateGroupChat,
  useCreateSecretChat,
  useJoinChat,
} from '../model/use-create-chat'

type Tab = 'direct' | 'secret' | 'group' | 'channel' | 'join'

export function NewChatDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const t = useTranslate()
  const router = useRouter()
  const createGroup = useCreateGroupChat()
  const createSecret = useCreateSecretChat()
  const joinChat = useJoinChat()

  const [tab, setTab] = useState<Tab>('direct')
  const [username, setUsername] = useState('')
  const [title, setTitle] = useState('')
  const [members, setMembers] = useState('')
  const [code, setCode] = useState('')
  const [error, setError] = useState<string | null>(null)

  function reset() {
    setUsername('')
    setTitle('')
    setMembers('')
    setCode('')
    setError(null)
  }

  function describe(caught: unknown): string {
    if (caught instanceof ProtocolError) {
      // Its own message because it is the one refusal here with an ANSWER. Falling
      // through to the generic business-error text would make a purchasable feature
      // look broken — which is exactly why the server gives it a code of its own
      // instead of reusing FORBIDDEN.
      if (caught.code === ErrorCode.PREMIUM_REQUIRED) return t('secret.premiumRequired')
      if (caught.code === ErrorCode.NOT_FOUND) return t('error.userNotFound')
      if (caught.class === 'throttle') return t('error.rateLimited')
      if (caught.class === 'business') return caught.message || t('error.unknown')
    }
    return t('error.unknown')
  }

  async function submit() {
    setError(null)
    try {
      if (tab === 'direct') {
        const target = directChatTarget(username)
        if (!target) return
        // No chat exists yet: the gateway creates it on the first SEND to the
        // handle, so we route to a compose screen carrying the target.
        router.push(`/chats/new?to=${encodeURIComponent(target.slice(1))}`)
      } else if (tab === 'secret') {
        const target = directChatTarget(username)
        if (!target) return
        // Unlike a direct chat, this one IS created by a message: the row has to exist
        // before either side can start a session in it, so there is nothing to defer
        // to a first send.
        const { info } = await createSecret.mutateAsync(target)
        router.push(`/chats/${info.chatId}`)
      } else if (tab === 'join') {
        const chatId = await joinChat.mutateAsync(code)
        if (chatId) router.push(`/chats/${chatId}`)
      } else {
        const info = await createGroup.mutateAsync({
          kind: tab,
          title,
          members: members
            .split(',')
            .map((value) => value.trim())
            .filter(Boolean),
        })
        router.push(`/chats/${info.chatId}`)
      }
      reset()
      onClose()
    } catch (caught) {
      setError(describe(caught))
    }
  }

  const tabs: { id: Tab; label: string }[] = [
    { id: 'direct', label: t('compose.direct') },
    { id: 'secret', label: t('secret.new') },
    { id: 'group', label: t('compose.group') },
    { id: 'channel', label: t('compose.channel') },
    { id: 'join', label: t('compose.join') },
  ]

  const pending = createGroup.isPending || createSecret.isPending || joinChat.isPending
  const canSubmit =
    tab === 'direct' || tab === 'secret'
      ? username.trim().length > 0
      : tab === 'join'
        ? code.trim().length > 0
        : title.trim().length > 0

  return (
    <Modal open={open} onClose={onClose} title={t('compose.title')}>
      <div className="bg-surface-sunken mb-4 flex gap-1 rounded-xl p-1">
        {tabs.map((item) => (
          <button
            key={item.id}
            type="button"
            onClick={() => {
              setTab(item.id)
              setError(null)
            }}
            className={cn(
              'flex-1 rounded-lg px-2 py-1.5 text-xs font-medium transition-colors',
              tab === item.id
                ? 'bg-surface-raised text-ink shadow-sm'
                : 'text-ink-muted hover:text-ink',
            )}
          >
            {item.label}
          </button>
        ))}
      </div>

      <div className="flex flex-col gap-3">
        {tab === 'direct' && (
          <TextField
            label={t('compose.username')}
            hint={t('compose.directHint')}
            prefix="@"
            value={username}
            autoCapitalize="none"
            spellCheck={false}
            onChange={(event) => setUsername(event.target.value)}
          />
        )}

        {tab === 'secret' && (
          <>
            <TextField
              label={t('compose.username')}
              hint={t('compose.directHint')}
              prefix="@"
              value={username}
              autoCapitalize="none"
              spellCheck={false}
              onChange={(event) => setUsername(event.target.value)}
            />
            <p className="text-ink-faint text-xs">{t('secret.newHint')}</p>
          </>
        )}

        {(tab === 'group' || tab === 'channel') && (
          <>
            <TextField
              label={t('compose.chatTitle')}
              value={title}
              maxLength={128}
              onChange={(event) => setTitle(event.target.value)}
            />
            <TextField
              label={t('compose.members')}
              hint={t('compose.membersHint')}
              placeholder="alice, bob"
              value={members}
              autoCapitalize="none"
              spellCheck={false}
              onChange={(event) => setMembers(event.target.value)}
            />
            <p className="text-ink-faint text-xs">
              {tab === 'group' ? t('compose.groupHint') : t('compose.channelHint')}
            </p>
          </>
        )}

        {tab === 'join' && (
          <TextField
            label={t('compose.codeOrHandle')}
            hint={t('compose.joinHint')}
            value={code}
            autoCapitalize="none"
            spellCheck={false}
            onChange={(event) => setCode(event.target.value)}
          />
        )}

        {error && <ErrorNote>{error}</ErrorNote>}

        <div className="mt-1 flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose} type="button">
            {t('common.cancel')}
          </Button>
          <Button onClick={submit} loading={pending} disabled={!canSubmit} type="button">
            {tab === 'direct'
              ? t('compose.open')
              : tab === 'secret'
                ? t('secret.create')
                : tab === 'join'
                  ? t('compose.join')
                  : t('compose.create')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}
