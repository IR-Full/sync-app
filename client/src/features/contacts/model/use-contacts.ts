'use client'

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'

import { useUserDirectory } from '@/entities/user'
import { MsgType, queryKeys, useIsConnected, useSynapseClient, type Wire } from '@/shared/api'

export interface Contact {
  userId: string
  name: string
  blocked: boolean
  updatedAt: number
}

/**
 * The address book.
 *
 * CONTACT_SYNC is incremental by design — it takes a cursor and returns what
 * changed since — but this client asks for everything (`since: 0`) on each
 * fetch. The list is small, the query cache already avoids refetching it, and a
 * full snapshot removes a whole class of "my cursor drifted" bugs. The
 * incremental path is there to grow into if a contact list ever gets large.
 */
export function useContacts() {
  const client = useSynapseClient()
  const connected = useIsConnected()

  const query = useQuery({
    queryKey: queryKeys.contacts(),
    enabled: connected,
    staleTime: 60_000,
    queryFn: async (): Promise<Contact[]> => {
      const reply = await client.request<Wire.ContactList>(
        MsgType.CONTACT_SYNC,
        { since: 0 },
        { expect: MsgType.CONTACT_LIST },
      )
      return reply.body.contacts.map((contact) => ({
        userId: contact.userId,
        name: contact.name,
        blocked: contact.blocked,
        updatedAt: contact.updatedAt,
      }))
    },
  })

  // Contacts are the only source that maps a user id to a human name, so feed
  // the directory that renders sender labels everywhere else.
  const contacts = query.data
  useEffect(() => {
    if (!contacts?.length) return
    useUserDirectory.getState().upsertMany(
      contacts.map((contact) => ({
        userId: contact.userId,
        name: contact.name || undefined,
      })),
    )
  }, [contacts])

  return query
}

export function useAddContact() {
  const client = useSynapseClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async ({ target, name }: { target: string; name?: string }) => {
      const handle = target.startsWith('@') ? target : `@${target}`
      const reply = await client.request<Wire.ContactList>(
        MsgType.CONTACT_ADD,
        { target: handle, name: name ?? '' },
        { expect: MsgType.CONTACT_LIST },
      )
      return reply.body.contacts[0] ?? null
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.contacts() }),
  })
}

export function useRemoveContact() {
  const client = useSynapseClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (target: string) => {
      await client.request(
        MsgType.CONTACT_REMOVE,
        { target: target.startsWith('@') ? target : `@${target}` },
        { expect: MsgType.CONTACT_LIST },
      )
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.contacts() }),
  })
}

/**
 * Block or unblock.
 *
 * Blocking is enforced in both directions on the server: neither party can
 * resolve the direct chat afterwards, so a blocked user cannot message you and
 * you cannot read their replies by reopening the chat.
 */
export function useBlockUser() {
  const client = useSynapseClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async ({ target, blocked }: { target: string; blocked: boolean }) => {
      client.send(MsgType.BLOCK, {
        target: target.startsWith('@') ? target : `@${target}`,
        blocked,
      })
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.contacts() }),
  })
}
