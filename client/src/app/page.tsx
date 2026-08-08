'use client'

import { useRouter } from 'next/navigation'
import { useEffect } from 'react'

import { useSessionStore } from '@/entities/session'
import { useTranslate } from '@/shared/i18n'
import { Spinner } from '@/shared/ui'

/**
 * Entry point: routes to the app or the login screen once the stored session
 * has been read. Client-side, because the session lives in localStorage — the
 * server has no way to know whether this browser is signed in.
 */
export default function IndexPage() {
  const router = useRouter()
  const t = useTranslate()
  const status = useSessionStore((state) => state.status)

  useEffect(() => {
    if (status === 'unknown') return
    router.replace(status === 'authenticated' ? '/chats' : '/login')
  }, [status, router])

  return (
    <div className="flex h-dvh flex-col items-center justify-center gap-3">
      <Spinner />
      <p className="text-ink-muted text-sm">{t('auth.restoring')}</p>
    </div>
  )
}
