'use client'

import { useRouter } from 'next/navigation'
import { useEffect } from 'react'

import { useSessionStore } from '@/entities/session'
import { AuthForm } from '@/features/auth'
import { useTranslate } from '@/shared/i18n'

export default function LoginPage() {
  const t = useTranslate()
  const router = useRouter()
  const status = useSessionStore((state) => state.status)

  // A signed-in user landing here (back button, bookmark) goes straight through.
  useEffect(() => {
    if (status === 'authenticated') router.replace('/chats')
  }, [status, router])

  return (
    <main className="flex min-h-dvh items-center justify-center p-4">
      <div className="w-full max-w-sm">
        <div className="mb-8 text-center">
          <div className="bg-accent text-accent-ink mx-auto mb-3 flex size-12 items-center justify-center rounded-2xl">
            <svg
              viewBox="0 0 24 24"
              className="size-6"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.8"
            >
              <path
                d="M4 5.5A1.5 1.5 0 015.5 4h13A1.5 1.5 0 0120 5.5v9a1.5 1.5 0 01-1.5 1.5H9l-5 4V5.5z"
                strokeLinejoin="round"
              />
            </svg>
          </div>
          <h1 className="text-ink text-2xl font-semibold">{t('app.name')}</h1>
          <p className="text-ink-muted mt-1 text-sm">{t('auth.tagline')}</p>
        </div>

        <div className="border-line bg-surface rounded-2xl border p-6">
          <AuthForm />
        </div>
      </div>
    </main>
  )
}
