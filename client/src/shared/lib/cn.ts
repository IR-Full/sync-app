/**
 * Joins class names, dropping falsy entries.
 *
 * Intentionally not `tailwind-merge`: these components compose classes in one
 * direction (base first, caller's `className` last), so later utilities already
 * win by CSS source order and a full conflict resolver would be dead weight.
 */
export type ClassValue = string | false | null | undefined

export function cn(...values: ClassValue[]): string {
  return values.filter(Boolean).join(' ')
}
