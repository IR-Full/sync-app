'use client'

import Link from 'next/link'
import { usePathname } from 'next/navigation'
import type { ReactNode } from 'react'

import { useSessionStore } from '@/entities/session'
import { useLogout } from '@/features/auth'
import { useTranslate } from '@/shared/i18n'
import { cn } from '@/shared/lib/cn'
import { Avatar } from '@/shared/ui'
import { ConnectionBanner } from '@/widgets/connection-banner'

/**
 * Two-pane layout: chat list beside the active conversation on desktop, one
 * pane at a time on mobile. Which pane shows on small screens is derived from
 * the route — a chat route hides the list, `/chats` hides the (empty) detail —
 * so the browser's back button moves between them for free.
 */
export function AppShell({ list, children }: { list: ReactNode; children: ReactNode }) {
  const t = useTranslate()
  const pathname = usePathname()
  const session = useSessionStore((state) => state.session)
  const logout = useLogout()

  const onDetail = /^\/chats\/.+/.test(pathname ?? '')
  const username = session?.displayName || session?.username || ''

  return (
    <div className="bg-surface-sunken flex h-dvh flex-col">
      <ConnectionBanner />

      <div className="flex min-h-0 flex-1">
        <aside
          className={cn(
            'border-line bg-surface w-full shrink-0 flex-col border-r md:flex md:w-80 lg:w-96',
            onDetail ? 'hidden md:flex' : 'flex',
          )}
        >
          <header className="border-line flex items-center gap-2 border-b px-3 py-2.5">
            <Link
              href="/profile"
              className="hover:bg-surface-hover flex min-w-0 flex-1 items-center gap-2 rounded-lg p-1"
            >
              <Avatar seed={session?.userId ?? ''} name={username || '?'} size="small" />
              <span className="text-ink truncate text-sm font-medium">@{username}</span>
            </Link>

            <Link
              href="/contacts"
              aria-label={t('contacts.manage')}
              title={t('contacts.manage')}
              className="rounded-lg p-2 text-ink-muted transition-colors hover:bg-surface-hover hover:text-ink"
            >
              <svg viewBox="0 0 20 20" className="size-5" fill="none" stroke="currentColor" strokeWidth="1.5">
                <circle cx="8" cy="7" r="3" />
                <path d="M2.5 16.5c0-2.8 2.5-4.5 5.5-4.5s5.5 1.7 5.5 4.5M14 8h4M16 6v4" strokeLinecap="round" />
              </svg>
            </Link>

            <Link
              href="/settings"
              aria-label={t('nav.settings')}
              title={t('nav.settings')}
              className="text-ink-muted hover:bg-surface-hover hover:text-ink rounded-lg p-2 transition-colors"
            >
              <svg
                viewBox="0 0 20 20"
                className="size-5"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
              >
                <circle cx="10" cy="10" r="2.6" />
                <path
                  d="M10 2.5v1.8M10 15.7v1.8M17.5 10h-1.8M4.3 10H2.5M15.3 4.7l-1.3 1.3M6 14l-1.3 1.3M15.3 15.3L14 14M6 6L4.7 4.7"
                  strokeLinecap="round"
                />
              </svg>
            </Link>

            <button
              type="button"
              onClick={logout}
              aria-label={t('nav.logout')}
              title={t('nav.logout')}
              className="text-ink-muted hover:bg-surface-hover hover:text-danger rounded-lg p-2 transition-colors"
            >
              <svg
                viewBox="0 0 20 20"
                className="size-5"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
              >
                <path
                  d="M8 17H4.5A1.5 1.5 0 013 15.5v-11A1.5 1.5 0 014.5 3H8M13 13.5L16.5 10 13 6.5M16 10H7.5"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                />
              </svg>
            </button>
          </header>

          <div className="min-h-0 flex-1">{list}</div>
        </aside>

        <main className={cn('min-w-0 flex-1', onDetail ? 'flex' : 'hidden md:flex')}>
          {children}
        </main>
      </div>
    </div>
  )
}
