package e2e

import (
	"strings"
	"testing"
)

// makeIdentity returns a deterministic-looking pair of identity public keys for
// tests (contents need only be stable, not real curve points, for the
// fingerprint function).
func makeIdentity(seed byte) (ik, sk []byte) {
	ik = make([]byte, 32)
	sk = make([]byte, 32)
	for i := range ik {
		ik[i] = seed + byte(i)
		sk[i] = seed ^ byte(i)
	}
	return ik, sk
}

func TestSafetyNumberSymmetric(t *testing.T) {
	aIK, aSK := makeIdentity(1)
	bIK, bSK := makeIdentity(9)

	// Alice computes with herself as local; Bob computes with himself as local.
	fromAlice := SafetyNumber("alice", aIK, aSK, "bob", bIK, bSK)
	fromBob := SafetyNumber("bob", bIK, bSK, "alice", aIK, aSK)

	if fromAlice != fromBob {
		t.Fatalf("safety number must be identical for both parties:\n  alice=%q\n  bob  =%q", fromAlice, fromBob)
	}
}

func TestSafetyNumberFormat(t *testing.T) {
	aIK, aSK := makeIdentity(1)
	bIK, bSK := makeIdentity(9)
	sn := SafetyNumber("alice", aIK, aSK, "bob", bIK, bSK)

	groups := strings.Fields(sn)
	if len(groups) != 12 { // two 30-digit fingerprints × six 5-digit groups
		t.Fatalf("expected 12 groups, got %d in %q", len(groups), sn)
	}
	for _, g := range groups {
		if len(g) != 5 {
			t.Errorf("group %q is not 5 digits", g)
		}
		for _, r := range g {
			if r < '0' || r > '9' {
				t.Errorf("group %q contains a non-digit", g)
			}
		}
	}
}

func TestSafetyNumberDetectsKeySwap(t *testing.T) {
	aIK, aSK := makeIdentity(1)
	bIK, bSK := makeIdentity(9)
	honest := SafetyNumber("alice", aIK, aSK, "bob", bIK, bSK)

	// A MITM substitutes a different identity key for Bob.
	mIK, mSK := makeIdentity(42)
	mitm := SafetyNumber("alice", aIK, aSK, "bob", mIK, mSK)

	if honest == mitm {
		t.Fatal("safety number did not change when Bob's identity key was swapped (MITM would be undetectable)")
	}
}

func TestSafetyNumberDeterministic(t *testing.T) {
	aIK, aSK := makeIdentity(1)
	bIK, bSK := makeIdentity(9)
	first := SafetyNumber("alice", aIK, aSK, "bob", bIK, bSK)
	second := SafetyNumber("alice", aIK, aSK, "bob", bIK, bSK)
	if first != second {
		t.Fatal("safety number must be deterministic")
	}
}

func TestSafetyNumberStableIDMatters(t *testing.T) {
	// Same keys but different user ids must not collide (guards against a shared
	// or reused key yielding an identical fingerprint).
	ik, sk := makeIdentity(1)
	bIK, bSK := makeIdentity(9)
	one := SafetyNumber("alice", ik, sk, "bob", bIK, bSK)
	two := SafetyNumber("carol", ik, sk, "bob", bIK, bSK)
	if one == two {
		t.Fatal("fingerprint should incorporate the stable id")
	}
}
