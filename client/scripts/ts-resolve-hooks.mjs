import { existsSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const CANDIDATES = ['.ts', '.tsx', '/index.ts', '/index.tsx']

/** Mirrors the "@/*" -> "src/*" path alias from tsconfig.json. */
const SRC = new URL('../src/', import.meta.url)

export async function resolve(specifier, context, nextResolve) {
  if (specifier.startsWith('@/')) {
    const base = new URL(specifier.slice(2), SRC)
    for (const ext of ['', ...CANDIDATES]) {
      const candidate = new URL(base.href + ext)
      if (existsSync(fileURLToPath(candidate))) {
        return nextResolve(candidate.href, context)
      }
    }
  }
  try {
    return await nextResolve(specifier, context)
  } catch (error) {
    if (!specifier.startsWith('.')) {
      // Bare specifiers into CJS subpaths ("protobufjs/light") resolve under a
      // bundler but need the explicit .js under Node ESM.
      return nextResolve(`${specifier}.js`, context)
    }
    const base = new URL(specifier, context.parentURL)
    for (const ext of CANDIDATES) {
      const candidate = new URL(base.href + ext)
      if (existsSync(fileURLToPath(candidate))) {
        return nextResolve(candidate.href, context)
      }
    }
    throw error
  }
}
