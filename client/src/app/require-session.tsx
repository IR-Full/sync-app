'use client'

import { useRouter } from 'next/navigation'
import { useEffect, type ReactNode } from 'react'

import { useSessionStore } from '@/entities/session'
import { useTranslate } from '@/shared/i18n'
import { Spinner } from '@/shared/ui'

/**
 * Client-side route guard.
 *
 * There is no server-side check to make: authentication happens over the
 * WebSocket and the credential lives in localStorage, so the server rendering
 * this page cannot know whether the visitor is signed in. The real enforcement
 * is the gateway itself — every protocol request on an unauthenticated
 * connection is rejected — and this guard is purely about not showing an empty
 * shell to a signed-out visitor.
 */
export function RequireSession({ children }: { children: ReactNode }) {
  const router = useRouter()
  const t = useTranslate()
  const status = useSessionStore((state) => state.status)

  useEffect(() => {
    if (status === 'anonymous') router.replace('/login')
  }, [status, router])

  if (status !== 'authenticated') {
    return (
      <div className="flex h-dvh flex-col items-center justify-center gap-3">
        <Spinner />
        <p className="text-ink-muted text-sm">{t('auth.restoring')}</p>
      </div>
    )
  }

  return children
}
