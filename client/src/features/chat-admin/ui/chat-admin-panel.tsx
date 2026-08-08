'use client'

import { useState } from 'react'

import { ProtocolError } from '@/shared/api'
import { useTranslate } from '@/shared/i18n'
import { Button, ErrorNote, Modal, Spinner, TextField } from '@/shared/ui'

import {
  useChatExport,
  useCreateInvite,
  useInviteLinks,
  useRevokeInvite,
  useSetChatUsername,
  useSetMemberRole,
  type MemberRole,
} from '../model/use-chat-admin'

const ROLES: MemberRole[] = ['member', 'admin', 'owner']

/**
 * Owner/admin controls for a chat.
 *
 * Every operation here is authorised server-side; a member without rights gets a
 * `forbidden` back, which is surfaced as a plain message rather than hidden —
 * the client cannot know the caller's role, because no protocol message reports
 * it.
 */
export function ChatAdminPanel({
  chatId,
  open,
  onClose,
}: {
  chatId: string
  open: boolean
  onClose: () => void
}) {
  const t = useTranslate()
  const invites = useInviteLinks(chatId)
  const createInvite = useCreateInvite(chatId)
  const revokeInvite = useRevokeInvite(chatId)
  const setRole = useSetMemberRole(chatId)
  const setUsername = useSetChatUsername(chatId)
  const exportChat = useChatExport()

  const [handle, setHandle] = useState('')
  const [maxUses, setMaxUses] = useState('0')
  const [expiresHours, setExpiresHours] = useState('0')
  const [userId, setUserId] = useState('')
  const [role, setRoleValue] = useState<MemberRole>('member')
  const [message, setMessage] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  function report(caught: unknown) {
    setError(
      caught instanceof ProtocolError && caught.class === 'business'
        ? (caught.message ?? t('admin.onlyAdmins'))
        : caught instanceof ProtocolError && caught.code === 3000
          ? t('admin.onlyAdmins')
          : t('error.unknown'),
    )
  }

  async function run(action: () => Promise<unknown>, success?: string) {
    setError(null)
    setMessage(null)
    try {
      await action()
      if (success) setMessage(success)
    } catch (caught) {
      report(caught)
    }
  }

  return (
    <Modal open={open} onClose={onClose} title={t('admin.title')}>
      <div className="flex flex-col gap-5">
        <section>
          <h3 className="mb-2 text-sm font-semibold">{t('admin.handle')}</h3>
          <div className="flex items-end gap-2">
            <TextField
              prefix="@"
              hint={t('admin.handleHint')}
              value={handle}
              autoCapitalize="none"
              spellCheck={false}
              onChange={(event) => setHandle(event.target.value)}
              className="flex-1"
            />
            <Button
              loading={setUsername.isPending}
              onClick={() => run(() => setUsername.mutateAsync(handle), t('profile.saved'))}
            >
              {t('profile.save')}
            </Button>
          </div>
        </section>

        <section>
          <h3 className="mb-2 text-sm font-semibold">{t('admin.invites')}</h3>
          <div className="flex items-end gap-2">
            <TextField
              label={t('admin.maxUses')}
              type="number"
              min={0}
              value={maxUses}
              onChange={(event) => setMaxUses(event.target.value)}
              className="flex-1"
            />
            <TextField
              label={t('admin.expiresHours')}
              type="number"
              min={0}
              value={expiresHours}
              onChange={(event) => setExpiresHours(event.target.value)}
              className="flex-1"
            />
            <Button
              loading={createInvite.isPending}
              onClick={() =>
                run(() => {
                  const hours = Number(expiresHours) || 0
                  return createInvite.mutateAsync({
                    maxUses: Number(maxUses) || 0,
                    expiresAt: hours > 0 ? Date.now() + hours * 3_600_000 : 0,
                  })
                })
              }
            >
              {t('admin.createInvite')}
            </Button>
          </div>

          <div className="mt-3">
            {invites.isLoading ? (
              <Spinner className="size-4" />
            ) : !invites.data?.length ? (
              <p className="text-ink-faint text-xs">{t('admin.noInvites')}</p>
            ) : (
              <ul className="flex flex-col gap-1">
                {invites.data.map((link) => (
                  <li
                    key={link.code}
                    className="bg-surface-sunken flex items-center gap-2 rounded-lg px-2 py-1.5"
                  >
                    <code className="min-w-0 flex-1 truncate font-mono text-xs">
                      {link.code}
                    </code>
                    <span className="text-ink-faint shrink-0 text-[11px]">
                      {t('admin.uses', {
                        used: link.uses,
                        max: link.maxUses || t('admin.unlimited'),
                      })}
                    </span>
                    <Button
                      size="small"
                      variant="ghost"
                      onClick={() => void navigator.clipboard.writeText(link.code)}
                    >
                      {t('common.copy')}
                    </Button>
                    <Button
                      size="small"
                      variant="ghost"
                      onClick={() => run(() => revokeInvite.mutateAsync(link.code))}
                    >
                      {t('admin.revoke')}
                    </Button>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </section>

        <section>
          <h3 className="mb-2 text-sm font-semibold">{t('admin.setRole')}</h3>
          <div className="flex items-end gap-2">
            <TextField
              label={t('admin.userId')}
              value={userId}
              onChange={(event) => setUserId(event.target.value)}
              className="flex-1"
            />
            <select
              aria-label={t('admin.role')}
              value={role}
              onChange={(event) => setRoleValue(event.target.value as MemberRole)}
              className="border-line bg-surface-raised h-10 rounded-xl border px-2 text-sm"
            >
              {ROLES.map((value) => (
                <option key={value} value={value}>
                  {value}
                </option>
              ))}
            </select>
            <Button
              loading={setRole.isPending}
              disabled={!userId.trim()}
              onClick={() =>
                run(
                  () => setRole.mutateAsync({ userId: userId.trim(), role }),
                  t('profile.saved'),
                )
              }
            >
              {t('admin.setRole')}
            </Button>
          </div>
        </section>

        <section>
          <h3 className="mb-2 text-sm font-semibold">{t('admin.export')}</h3>
          <Button
            variant="secondary"
            loading={exportChat.isPending}
            onClick={() =>
              run(async () => {
                const dump = await exportChat.mutateAsync(chatId)
                // Hand the dump to the user as a file rather than dropping it in
                // the console — an export exists to leave the app.
                const blob = new Blob([JSON.stringify(dump, null, 2)], {
                  type: 'application/json',
                })
                const url = URL.createObjectURL(blob)
                const anchor = document.createElement('a')
                anchor.href = url
                anchor.download = `chat-${chatId}.json`
                anchor.click()
                URL.revokeObjectURL(url)
                setMessage(t('admin.exported', { count: dump.messages.length }))
              })
            }
          >
            {exportChat.isPending ? t('admin.exporting') : t('admin.export')}
          </Button>
        </section>

        {message && <p className="text-success text-sm">{message}</p>}
        {error && <ErrorNote>{error}</ErrorNote>}
      </div>
    </Modal>
  )
}
