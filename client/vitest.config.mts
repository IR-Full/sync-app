import { fileURLToPath } from 'node:url'

import { defineConfig } from 'vitest/config'

/**
 * Test config for the web client.
 *
 * Kept separate from `next.config.ts`: Next builds the app, Vitest runs the
 * logic underneath it. Nothing here participates in the production bundle.
 */
export default defineConfig({
  resolve: {
    // Mirrors the `@/*` path alias in tsconfig.json, so a test imports a module
    // by the same specifier the source does.
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  test: {
    // jsdom because the protocol layer touches WebSocket/DOM globals and the
    // stores persist through localStorage. The node environment would need each
    // of those faked, which is a worse approximation of a browser than jsdom is.
    environment: 'jsdom',
    // Supplies IndexedDB and crypto.subtle, which jsdom lacks and the key vault
    // needs. See the file for why they are real implementations, not stubs.
    setupFiles: ['./vitest.setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
    globals: true,
    coverage: {
      provider: 'v8',
      reporter: ['text-summary', 'json-summary'],
      include: ['src/**/*.{ts,tsx}'],
      exclude: [
        // Generated from the server's body.proto — testing it tests protobufjs.
        'src/shared/api/protocol/generated/**',
        // Type-only modules compile to nothing, so they report as 0% forever.
        'src/**/*.d.ts',
        'src/**/types.ts',
        // Next.js route/layout files are framework wiring exercised by the build.
        'src/app/**/layout.tsx',
        'src/app/**/page.tsx',
        // Test-only harnesses. They are not shipped, and counting them would
        // inflate the number with code that exists to measure other code.
        'src/**/fake-socket.ts',
        'src/**/test-harness.tsx',
        'src/**/*.test-helper.ts',
      ],
    },
  },
})
