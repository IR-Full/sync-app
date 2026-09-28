import { describe, expect, it } from 'vitest'

import { fromBase64 } from './codec'
import { safetyNumber, type SafetyIdentity } from './safety'

/**
 * Fixed, obviously-fake keys. The point of the vector is that it is reproducible
 * on both sides from the file itself rather than from a keygen, so the Go and
 * TypeScript implementations can be compared without running them together.
 */
const ALICE: SafetyIdentity = {
  stableId: 'alice',
  identityKey: fromBase64('AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8='),
  signingKey: fromBase64('ZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXp7fH1+f4CBgoM='),
}

const BOB: SafetyIdentity = {
  stableId: 'bob',
  identityKey: fromBase64('//79/Pv6+fj39vX08/Lx8O/u7ezr6uno5+bl5OPi4eA='),
  signingKey: fromBase64('AAMGCQwPEhUYGx4hJCcqLTAzNjk8P0JFSEtOUVRXWl0='),
}

/**
 * Produced by `e2e.SafetyNumber` in `server/pkg/e2e/safety.go` against the keys
 * above. If this ever disagrees, the two implementations show DIFFERENT numbers
 * for the same pair of identities — and the failure looks exactly like a
 * man-in-the-middle to the two people comparing them, which is the worst
 * possible way for a bug here to present.
 */
const GO_VECTOR = '30610 41453 91725 20170 87649 47234 72278 47363 31527 20120 04298 35078'

describe('safetyNumber', () => {
  it('matches the Go implementation byte for byte', () => {
    expect(safetyNumber(ALICE, BOB)).toBe(GO_VECTOR)
  })

  /**
   * The two endpoints disagree about which side is "local", so the combination
   * has to be canonical. If it were not, each side would read out a different
   * number and conclude they were under attack.
   */
  it('is symmetric', () => {
    expect(safetyNumber(BOB, ALICE)).toBe(safetyNumber(ALICE, BOB))
  })

  it('renders twelve groups of five digits', () => {
    const groups = safetyNumber(ALICE, BOB).split(' ')
    expect(groups).toHaveLength(12)
    for (const group of groups) expect(group).toMatch(/^\d{5}$/)
  })

  /** The whole signal: a swapped identity key changes the number. */
  it('changes when either identity key changes', () => {
    const impostor: SafetyIdentity = {
      ...BOB,
      identityKey: fromBase64('AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8='),
    }
    expect(safetyNumber(ALICE, impostor)).not.toBe(safetyNumber(ALICE, BOB))
  })

  /** ...and so does a swapped signing key: both halves are bound in. */
  it('changes when either signing key changes', () => {
    const impostor: SafetyIdentity = { ...BOB, signingKey: ALICE.signingKey }
    expect(safetyNumber(ALICE, impostor)).not.toBe(safetyNumber(ALICE, BOB))
  })

  /**
   * The stable id is salted in so two users who somehow ended up with the same
   * key pair still get distinct fingerprints.
   */
  it('changes when the stable id changes', () => {
    expect(safetyNumber({ ...ALICE, stableId: 'alice2' }, BOB)).not.toBe(
      safetyNumber(ALICE, BOB),
    )
  })
})
