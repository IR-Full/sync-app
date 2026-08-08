'use client'

import { useTranslate } from '@/shared/i18n'
import { EmptyState } from '@/shared/ui'

/** Desktop placeholder while no conversation is open. */
export default function ChatsIndexPage() {
  const t = useTranslate()
  return (
    <div className="bg-surface flex flex-1 items-center justify-center">
      <EmptyState title={t('chat.selectPrompt')} description={t('chats.emptyHint')} />
    </div>
  )
}
