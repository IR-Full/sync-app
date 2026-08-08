'use client'

import { useId, type InputHTMLAttributes, type ReactNode } from 'react'

import { cn } from '../lib/cn'

export interface TextFieldProps extends Omit<
  InputHTMLAttributes<HTMLInputElement>,
  'id' | 'prefix'
> {
  label?: ReactNode
  hint?: ReactNode
  error?: string | null
  /** rendered inside the input's left edge, e.g. an "@" sigil */
  prefix?: ReactNode
}

export function TextField({ label, hint, error, prefix, className, ...rest }: TextFieldProps) {
  const id = useId()
  const describedBy = error ? `${id}-error` : hint ? `${id}-hint` : undefined

  return (
    <div className="flex flex-col gap-1.5">
      {label && (
        <label htmlFor={id} className="text-ink text-sm font-medium">
          {label}
        </label>
      )}
      <div className="relative">
        {prefix && (
          <span className="text-ink-faint pointer-events-none absolute inset-y-0 left-3 flex items-center">
            {prefix}
          </span>
        )}
        <input
          {...rest}
          id={id}
          aria-invalid={error ? true : undefined}
          aria-describedby={describedBy}
          className={cn(
            'bg-surface-raised text-ink h-10 w-full rounded-xl border px-3 text-sm',
            'placeholder:text-ink-faint',
            'focus:outline-accent focus:outline-2 focus:outline-offset-0',
            'disabled:cursor-not-allowed disabled:opacity-60',
            prefix ? 'pl-8' : undefined,
            error ? 'border-danger' : 'border-line',
            className,
          )}
        />
      </div>
      {error ? (
        <p id={`${id}-error`} className="text-danger text-xs">
          {error}
        </p>
      ) : hint ? (
        <p id={`${id}-hint`} className="text-ink-faint text-xs">
          {hint}
        </p>
      ) : null}
    </div>
  )
}
