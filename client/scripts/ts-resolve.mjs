/**
 * Node module-resolution hook for running the .mts scripts directly.
 *
 * The app's sources use bundler-style extensionless imports ("./caps"), which
 * Next resolves but plain Node ESM does not. This hook appends the .ts/.tsx
 * extension (or /index.ts) so `node --experimental-strip-types` can execute the
 * real source files without a build step. Test tooling only — nothing here runs
 * in the browser.
 */
import { register } from 'node:module'
import { pathToFileURL } from 'node:url'

register('./ts-resolve-hooks.mjs', pathToFileURL('./scripts/'))
