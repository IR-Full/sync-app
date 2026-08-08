import { ChatWindow } from '@/widgets/chat-window'

/**
 * A conversation by id.
 *
 * `params` is a Promise in Next 16 — awaited here in a server component so the
 * client widget receives a plain string.
 */
export default async function ChatPage({ params }: { params: Promise<{ chatId: string }> }) {
  const { chatId } = await params
  return <ChatWindow target={chatId} />
}
