import type { ReactNode } from 'react'

import { ChatList } from '@/widgets/chat-list'
import { AppShell } from '@/widgets/app-shell'

import { RequireSession } from '../require-session'

/**
 * Every chat route shares one shell, so the chat list is not unmounted and
 * refetched each time the user opens a different conversation.
 */
export default function ChatsLayout({ children }: { children: ReactNode }) {
  return (
    <RequireSession>
      <AppShell list={<ChatList />}>{children}</AppShell>
    </RequireSession>
  )
}
