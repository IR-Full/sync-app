'use client'

import Link from 'next/link'
import { useState } from 'react'

import { labelForUser, useUserDirectory } from '@/entities/user'
import { ProtocolError } from '@/shared/api'
import { useTranslate } from '@/shared/i18n'
import { Avatar, Button, EmptyState, ErrorNote, Spinner, TextField } from '@/shared/ui'

import {
  useAddContact,
  useBlockUser,
  useContacts,
  useRemoveContact,
} from '../model/use-contacts'

/**
 * The address book.
 *
 * This is also the closest thing the protocol has to user search: there is no
 * "find users" message, only exact resolution of an "@username" inside another
 * request. Adding a contact is therefore how you turn a name you know into a
 * user id you can start a chat with.
 */
export function ContactsPanel() {
  const t = useTranslate()
  const { data: contacts, isLoading, isError, refetch } = useContacts()
  const directory = useUserDirectory((state) => state.users)

  const addContact = useAddContact()
  const removeContact = useRemoveContact()
  const blockUser = useBlockUser()

  const [target, setTarget] = useState('')
  const [name, setName] = useState('')
  const [error, setError] = useState<string | null>(null)

  async function submit() {
    const handle = target.trim().replace(/^@+/, '')
    if (!handle) return
    setError(null)
    try {
      await addContact.mutateAsync({ target: handle, name: name.trim() })
      setTarget('')
      setName('')
    } catch (caught) {
      setError(
        caught instanceof ProtocolError && caught.code === 3001
          ? t('error.userNotFound')
          : t('error.unknown'),
      )
    }
  }

  return (
    <div className="mx-auto flex w-full max-w-2xl flex-col gap-4 p-4">
      <section className="border-line bg-surface rounded-2xl border p-4">
        <h2 className="text-ink mb-3 text-sm font-semibold">{t('contacts.add')}</h2>
        <div className="flex flex-col gap-3 sm:flex-row sm:items-end">
          <TextField
            label={t('contacts.target')}
            prefix="@"
            value={target}
            autoCapitalize="none"
            spellCheck={false}
            onChange={(event) => setTarget(event.target.value)}
            className="flex-1"
          />
          <TextField
            label={t('contacts.name')}
            value={name}
            onChange={(event) => setName(event.target.value)}
            className="flex-1"
          />
          <Button onClick={submit} loading={addContact.isPending} disabled={!target.trim()}>
            {t('contacts.add')}
          </Button>
        </div>
        {error && <ErrorNote className="mt-2">{error}</ErrorNote>}
      </section>

      <section className="border-line bg-surface rounded-2xl border p-2">
        {isLoading ? (
          <div className="flex justify-center py-8">
            <Spinner />
          </div>
        ) : isError ? (
          <EmptyState
            title={t('error.title')}
            action={
              <Button size="small" variant="secondary" onClick={() => refetch()}>
                {t('error.retry')}
              </Button>
            }
          />
        ) : !contacts?.length ? (
          <EmptyState title={t('contacts.empty')} />
        ) : (
          <ul className="flex flex-col">
            {contacts.map((contact) => {
              const known = directory[contact.userId]
              const handle = (known?.username ?? contact.name ?? '').replace(/^@/, '')
              return (
                <li
                  key={contact.userId}
                  className="border-line flex items-center gap-3 border-b px-2 py-2.5 last:border-0"
                >
                  <Avatar
                    seed={contact.userId}
                    name={contact.name || contact.userId}
                    size="small"
                  />
                  <span className="min-w-0 flex-1">
                    <span className="text-ink block truncate text-sm">
                      {labelForUser(known, contact.userId)}
                    </span>
                    {contact.blocked && (
                      <span className="text-danger text-xs">{t('contacts.blocked')}</span>
                    )}
                  </span>

                  {handle && !contact.blocked && (
                    <Link
                      href={`/chats/new?to=${encodeURIComponent(handle)}`}
                      className="text-accent hover:bg-surface-hover rounded-lg px-2 py-1 text-xs"
                    >
                      {t('compose.open')}
                    </Link>
                  )}
                  <Button
                    size="small"
                    variant="ghost"
                    onClick={() =>
                      blockUser.mutate({
                        target: handle || contact.userId,
                        blocked: !contact.blocked,
                      })
                    }
                  >
                    {contact.blocked ? t('contacts.unblock') : t('contacts.block')}
                  </Button>
                  <Button
                    size="small"
                    variant="ghost"
                    onClick={() => removeContact.mutate(handle || contact.userId)}
                  >
                    {t('contacts.remove')}
                  </Button>
                </li>
              )
            })}
          </ul>
        )}
      </section>
    </div>
  )
}
