package com.synapse.messenger.domain.usecase

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * The bounds mirror `internal/auth` (username ≥ 3, password ≥ 6). Predicting a
 * rejection locally keeps a typo from reaching the gateway's per-username
 * brute-force throttle, which counts failed logins regardless of the reason.
 */
class CredentialRulesTest {

    @Test
    fun `accepts a valid pair`() {
        assertNull(CredentialRules.check("alice", "secret123"))
        assertNull(CredentialRules.check("@Alice", "secret123"))
        assertNull(CredentialRules.check("a.b_c-1", "secret123"))
    }

    @Test
    fun `rejects a short username`() {
        assertEquals(CredentialProblem.USERNAME_TOO_SHORT, CredentialRules.check("ab", "secret123"))
    }

    @Test
    fun `rejects characters that could not be addressed back`() {
        // Usernames travel as "@name" inside the protocol, so a space or an "@" in the
        // middle would produce a handle nobody could type.
        assertEquals(CredentialProblem.USERNAME_INVALID_CHARS, CredentialRules.check("al ice", "secret123"))
        assertEquals(CredentialProblem.USERNAME_INVALID_CHARS, CredentialRules.check("al@ice", "secret123"))
    }

    @Test
    fun `rejects a short password`() {
        assertEquals(CredentialProblem.PASSWORD_TOO_SHORT, CredentialRules.check("alice", "12345"))
    }

    @Test
    fun `normalizes the way the server does`() {
        // The gateway lowercases the username before looking it up.
        assertEquals("alice", CredentialRules.normalize(" @Alice "))
    }
}
