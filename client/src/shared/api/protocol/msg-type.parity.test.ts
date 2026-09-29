import { existsSync, readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { describe, expect, it } from 'vitest'

import { MsgType, msgTypeName } from './msg-type'

/**
 * Holds this client's message types against the server's.
 *
 * `msg-type.test.ts` next door pins numbers by hand, block by block. That is
 * worth having — it documents the allocation and catches a careless edit — but it
 * is a copy of the contract, so it agrees with itself forever: a type the server
 * adds is invisible to it, and so is a name the server spells differently. Both
 * of those are how a client drifts.
 *
 * So this file reads `server/pkg/wire/constants.go` and `types.go` instead, and
 * compares (number → name) PAIRS with both halves taken from the server: the
 * number from `constants.go`, the name from `MsgType.String()`. Deriving the name
 * from the Go identifier would be a guess, and a wrong one — `MsgTransportAck`
 * renders as `T_ACK`.
 *
 * The consequence of getting this wrong is quiet by design: the protocol's
 * extensibility rule is that an unknown type is SKIPPED, so a client one number
 * out decodes a `PINNED` frame as a `DRAFTS` one, or silently ignores a type it
 * never learned. Android and iOS carry the same check for the same reason.
 */

/** Walks up from this file so the test works from the repo root and from `client/`. */
function findServerWire(): string | null {
  let dir = dirname(new URL(import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1'))
  for (let i = 0; i < 12; i++) {
    const candidate = join(dir, 'server', 'pkg', 'wire')
    if (existsSync(join(candidate, 'constants.go'))) return candidate
    const parent = dirname(dir)
    if (parent === dir) break
    dir = parent
  }
  return null
}

const serverWire = findServerWire()

/** Go identifier → number. `Reserved` is 0, which is "no type at all". */
function serverNumbers(dir: string): Map<string, number> {
  const text = readFileSync(join(dir, 'constants.go'), 'utf8')
  const out = new Map<string, number>()
  for (const match of text.matchAll(/^\s*Msg(\w+)\s+MsgType = (\d+)/gm)) {
    if (match[1] === 'Reserved') continue
    out.set(match[1], Number(match[2]))
  }
  return out
}

/** Go identifier → the name the server writes into logs and metric labels. */
function serverNames(dir: string): Map<string, string> {
  const text = readFileSync(join(dir, 'types.go'), 'utf8')
  const out = new Map<string, string>()
  for (const match of text.matchAll(/case Msg(\w+):\s*\n\s*return "([A-Z0-9_]+)"/g)) {
    out.set(match[1], match[2])
  }
  return out
}

/** number → name, as this client defines the pair. */
function clientTypes(): Map<number, string> {
  const out = new Map<number, string>()
  for (const [name, value] of Object.entries(MsgType)) {
    if (name === 'RESERVED') continue
    out.set(value, name)
  }
  return out
}

// Skipped rather than failed when the server tree is absent: this package must
// stay testable on its own, and a test that cannot run is not the same as one
// that found a problem.
describe.skipIf(serverWire === null)('MsgType parity with the server', () => {
  it('declares every server type under the same number and name', () => {
    const dir = serverWire as string
    const names = serverNames(dir)
    const client = clientTypes()

    const disagreements: string[] = []
    for (const [identifier, number] of serverNumbers(dir)) {
      // A declared type with no branch in String() is the server's own problem,
      // and server/pkg/wire/names_test.go is what catches it. The next test makes
      // sure it cannot pass unnoticed here either.
      const expected = names.get(identifier)
      if (expected === undefined) continue

      const mine = client.get(number)
      if (mine === expected) continue
      disagreements.push(
        mine === undefined
          ? `${number} (${expected}) is missing here`
          : `${number} is "${mine}" here, "${expected}" on the server`,
      )
    }

    expect(disagreements.sort()).toEqual([])
  })

  it('finds a server name for every server type', () => {
    // The check above TRUSTS this: a type the server's own name table forgot would
    // drop silently out of the comparison rather than fail it.
    const dir = serverWire as string
    const names = serverNames(dir)
    const unnamed = [...serverNumbers(dir).keys()].filter((id) => !names.has(id))

    expect(unnamed.sort()).toEqual([])
  })

  it('names every type it declares', () => {
    // A type with no name renders as UNKNOWN(109) in a log, which is exactly the
    // moment somebody is reading the log to find out what arrived.
    const unnamed = [...clientTypes().keys()].filter((n) =>
      msgTypeName(n).startsWith('UNKNOWN'),
    )

    expect(unnamed).toEqual([])
  })

  it('uses each name for exactly one type', () => {
    // Two types under one name is worse than a missing one: a metric or a log
    // filter keyed on the name silently merges two different messages.
    const names = [...clientTypes().keys()].map(msgTypeName)

    expect(names.length).toBe(new Set(names).size)
  })
})
