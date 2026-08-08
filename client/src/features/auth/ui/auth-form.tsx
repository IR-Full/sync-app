'use client'

import { useRouter } from 'next/navigation'
import { useState, type FormEvent } from 'react'

import { useTranslate } from '@/shared/i18n'
import { Button, ErrorNote, TextField } from '@/shared/ui'

import { useAuthenticate } from '../model/use-auth'

export function AuthForm() {
  const t = useTranslate()
  const router = useRouter()
  const { authenticate, pending, error, clearError } = useAuthenticate()

  const [mode, setMode] = useState<'login' | 'register'>('login')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [touched, setTouched] = useState(false)

  const registering = mode === 'register'
  // Mirrors the server's own rule (internal/auth): 8 characters minimum.
  const passwordTooShort = registering && password.length > 0 && password.length < 8
  const canSubmit = username.trim().length > 0 && password.length > 0 && !passwordTooShort

  async function onSubmit(event: FormEvent) {
    event.preventDefault()
    setTouched(true)
    if (!canSubmit) return
    const ok = await authenticate({ username, password }, registering)
    if (ok) router.replace('/chats')
  }

  return (
    <form onSubmit={onSubmit} className="flex w-full flex-col gap-4" noValidate>
      <TextField
        label={t('auth.username')}
        hint={t('auth.usernameHint')}
        prefix="@"
        value={username}
        autoComplete="username"
        autoCapitalize="none"
        spellCheck={false}
        onChange={(event) => {
          setUsername(event.target.value)
          clearError()
        }}
        error={touched && !username.trim() ? t('error.required') : null}
      />

      <TextField
        label={t('auth.password')}
        hint={registering ? t('auth.passwordHint') : undefined}
        type="password"
        value={password}
        autoComplete={registering ? 'new-password' : 'current-password'}
        onChange={(event) => {
          setPassword(event.target.value)
          clearError()
        }}
        error={
          touched && !password
            ? t('error.required')
            : passwordTooShort
              ? t('auth.passwordHint')
              : null
        }
      />

      {error && <ErrorNote>{error}</ErrorNote>}

      <Button type="submit" loading={pending} disabled={!canSubmit}>
        {pending ? t('auth.submitting') : registering ? t('auth.signUp') : t('auth.signIn')}
      </Button>

      <button
        type="button"
        onClick={() => {
          setMode(registering ? 'login' : 'register')
          clearError()
          setTouched(false)
        }}
        className="text-accent text-sm underline-offset-4 hover:underline"
      >
        {registering ? t('auth.toLogin') : t('auth.toRegister')}
      </button>
    </form>
  )
}
