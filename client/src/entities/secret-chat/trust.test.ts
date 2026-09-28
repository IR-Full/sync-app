import { beforeEach, describe, expect, it } from 'vitest'

import { useTrustStore } from './trust'

const IK_A = 'AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8='
const SK_A = 'ZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXp7fH1+f4CBgoM='
const IK_B = '//79/Pv6+fj39vX08/Lx8O/u7ezr6uno5+bl5OPi4eA='

beforeEach(() => {
  localStorage.clear()
  useTrustStore.getState().reset()
  useTrustStore.getState().load('me')
})

describe('trust-on-first-use pinning', () => {
  /**
   * The first sighting cannot be checked against anything — a directory that
   * lies from the very beginning is believed, and that limit is inherent to
   * TOFU rather than a gap in this implementation.
   */
  it('reports a never-seen device as first use', () => {
    expect(useTrustStore.getState().verify('u1', 'd1', IK_A, SK_A)).toEqual({
      kind: 'first-use',
    })
  })

  it('recognises keys it has recorded', () => {
    useTrustStore.getState().accept('u1', 'd1', IK_A, SK_A)
    expect(useTrustStore.getState().verify('u1', 'd1', IK_A, SK_A)).toEqual({ kind: 'known' })
  })

  /**
   * The whole point: a server that starts handing out its own keys is caught on
   * every existing conversation at once, rather than never.
   */
  it('flags a changed identity key', () => {
    useTrustStore.getState().accept('u1', 'd1', IK_A, SK_A)
    const verdict = useTrustStore.getState().verify('u1', 'd1', IK_B, SK_A)
    expect(verdict.kind).toBe('changed')
    if (verdict.kind === 'changed') {
      // The recorded identity comes back so the UI can say what it expected.
      expect(verdict.pinned.identityKey).toBe(IK_A)
    }
  })

  /** Both halves are pinned: forging one and keeping the other must not pass. */
  it('flags a changed signing key', () => {
    useTrustStore.getState().accept('u1', 'd1', IK_A, SK_A)
    expect(useTrustStore.getState().verify('u1', 'd1', IK_A, IK_B).kind).toBe('changed')
  })

  /**
   * Pins are per DEVICE. Sharing one across a person's devices would mean a
   * reinstall on one phone invalidates the verification of another.
   */
  it('keeps devices independent', () => {
    useTrustStore.getState().accept('u1', 'd1', IK_A, SK_A)
    expect(useTrustStore.getState().verify('u1', 'd2', IK_B, SK_A)).toEqual({
      kind: 'first-use',
    })
  })

  it('keeps users independent', () => {
    useTrustStore.getState().accept('u1', 'd1', IK_A, SK_A)
    expect(useTrustStore.getState().verify('u2', 'd1', IK_A, SK_A)).toEqual({
      kind: 'first-use',
    })
  })

  /** A reinstall is resolved by accepting, which replaces the record. */
  it('accepts a replacement after a change', () => {
    useTrustStore.getState().accept('u1', 'd1', IK_A, SK_A)
    useTrustStore.getState().accept('u1', 'd1', IK_B, SK_A)
    expect(useTrustStore.getState().verify('u1', 'd1', IK_B, SK_A)).toEqual({ kind: 'known' })
  })

  it('forgets a pin on request', () => {
    useTrustStore.getState().accept('u1', 'd1', IK_A, SK_A)
    useTrustStore.getState().forget('u1', 'd1')
    expect(useTrustStore.getState().verify('u1', 'd1', IK_A, SK_A)).toEqual({
      kind: 'first-use',
    })
  })

  /**
   * Pins that vanish on reload protect nothing: the attack they catch is a key
   * that changes between sessions.
   */
  it('survives a reload', () => {
    useTrustStore.getState().accept('u1', 'd1', IK_A, SK_A)

    useTrustStore.getState().reset()
    useTrustStore.getState().load('me')

    expect(useTrustStore.getState().verify('u1', 'd1', IK_A, SK_A)).toEqual({ kind: 'known' })
  })

  /** Two accounts on one browser must not share each other's pins. */
  it('scopes pins to the signed-in account', () => {
    useTrustStore.getState().accept('u1', 'd1', IK_A, SK_A)

    useTrustStore.getState().load('someone-else')
    expect(useTrustStore.getState().verify('u1', 'd1', IK_A, SK_A)).toEqual({
      kind: 'first-use',
    })
  })

  /** Malformed base64 must read as a mismatch, never as a match. */
  it('treats unparseable key material as a change', () => {
    useTrustStore.getState().accept('u1', 'd1', IK_A, SK_A)
    expect(useTrustStore.getState().verify('u1', 'd1', '!!!not base64!!!', SK_A).kind).toBe(
      'changed',
    )
  })
})
