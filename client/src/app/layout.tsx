import type { Metadata, Viewport } from 'next'
import { Geist, Geist_Mono } from 'next/font/google'
import { headers } from 'next/headers'

import './globals.css'
import { Providers } from './providers'

const geistSans = Geist({ variable: '--font-geist-sans', subsets: ['latin', 'cyrillic'] })
const geistMono = Geist_Mono({ variable: '--font-geist-mono', subsets: ['latin'] })

/**
 * Locale-independent on purpose. This is a root-layout server export, evaluated
 * once per build with no request and no access to the store the locale lives in,
 * so it cannot follow the user's language — and a description hardcoded in one
 * language would simply be wrong for the other. English is the reference locale
 * (see `shared/i18n/dictionaries`), so it is the honest default here.
 */
export const metadata: Metadata = {
  title: 'SyncApp',
  description: 'Messenger on a custom binary protocol',
}

export const viewport: Viewport = {
  themeColor: [
    { media: '(prefers-color-scheme: light)', color: '#ffffff' },
    { media: '(prefers-color-scheme: dark)', color: '#14161b' },
  ],
}

/**
 * Applied before first paint so a dark-mode user never sees a white flash.
 * Inline because any deferred script runs after the browser has already painted.
 *
 * This is the one script in the app written by hand rather than emitted by
 * Next.js, so it is the one that has to carry the CSP nonce itself — everything
 * else Next.js stamps automatically once it sees the nonce in the request's
 * policy. Without the attribute it is simply blocked, and the flash it exists to
 * prevent comes back.
 */
const themeScript = `
(function () {
  try {
    var stored = JSON.parse(localStorage.getItem('SyncApp:theme') || '"system"');
    var dark = stored === 'dark' ||
      (stored === 'system' && matchMedia('(prefers-color-scheme: dark)').matches);
    if (dark) document.documentElement.classList.add('dark');
    document.documentElement.style.colorScheme = dark ? 'dark' : 'light';
  } catch (e) {}
})();
`

export default async function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  // Set by src/proxy.ts. Absent only if the proxy did not run for this path, in
  // which case there is no nonce-based policy to satisfy either.
  const nonce = (await headers()).get('x-nonce') ?? undefined

  return (
    <html
      lang="ru"
      suppressHydrationWarning
      className={`${geistSans.variable} ${geistMono.variable}`}
    >
      <head>
        {/*
          suppressHydrationWarning is for the NONCE, not for the script body.

          The browser blanks a nonce content attribute as soon as the element is
          parsed, keeping the value only on the `.nonce` IDL property — that is
          the spec's defence against `script[nonce^="a"]` style selectors
          exfiltrating it through CSS. React hydrates by comparing content
          attributes, so it reads "" where it rendered the real nonce and reports
          a mismatch it can do nothing about.

          The one on <html> does not cover this: it applies to that element's own
          attributes, and this script is two levels below it.
        */}
        <script
          nonce={nonce}
          suppressHydrationWarning
          dangerouslySetInnerHTML={{ __html: themeScript }}
        />
      </head>
      <body className="antialiased">
        <Providers>{children}</Providers>
      </body>
    </html>
  )
}
