import { redirect } from 'next/navigation'

import { ChatWindow } from '@/widgets/chat-window'

/**
 * Compose screen for a direct chat that does not exist yet.
 *
 * The gateway has no "create direct chat" message: a 1:1 chat is created when
 * the first message is addressed to "@username". So this route holds the handle
 * as the send target and the chat window swaps the URL for the real chat id once
 * the first SEND_ACK comes back.
 */
export default async function NewChatPage({
  searchParams,
}: {
  searchParams: Promise<{ to?: string }>
}) {
  const { to } = await searchParams
  const handle = to?.trim().replace(/^@+/, '')
  if (!handle) redirect('/chats')
  return <ChatWindow target={`@${handle}`} />
}
