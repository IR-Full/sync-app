/**
 * Builds with `.env.stage`.
 *
 * Next.js only auto-loads `.env.development` and `.env.production`, so a third
 * environment needs its variables put into the process explicitly. Doing it here
 * (rather than with a dotenv CLI dependency) keeps the toolchain to what is
 * already installed and works the same on Windows and POSIX.
 */
import { spawnSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const envFile = resolve(root, '.env.stage')

const env = { ...process.env }
for (const line of readFileSync(envFile, 'utf8').split('\n')) {
  const trimmed = line.trim()
  if (!trimmed || trimmed.startsWith('#')) continue
  const separator = trimmed.indexOf('=')
  if (separator === -1) continue
  env[trimmed.slice(0, separator).trim()] = trimmed.slice(separator + 1).trim()
}

const result = spawnSync('npx', ['next', 'build'], {
  cwd: root,
  env,
  stdio: 'inherit',
  shell: true,
})
process.exit(result.status ?? 1)
