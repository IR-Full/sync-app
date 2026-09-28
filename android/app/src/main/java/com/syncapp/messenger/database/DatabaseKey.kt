package com.syncapp.messenger.database

import android.annotation.SuppressLint
import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import android.util.Log
import androidx.core.content.edit
import dagger.hilt.android.qualifiers.ApplicationContext
import java.security.KeyStore
import java.security.SecureRandom
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The passphrase the local database is encrypted with.
 *
 * Thirty-two random bytes, generated once on this device and never sent
 * anywhere. They are not derived from the user's password: the database has to
 * open while the app is woken by a push with nobody present to type anything,
 * so there is no secret to derive from at that moment.
 *
 * What the passphrase is protecting is the copy on disk. Room's file sits in
 * app-private storage, which is enough against another app on a healthy device
 * and nothing at all against a rooted phone, a debuggable build reachable over
 * ADB, or someone holding the unlocked device — and the file holds the entire
 * message history in plain text, which is a good deal more than the session
 * tokens [com.syncapp.messenger.datastore.TokenCipher] already protects.
 *
 * The passphrase itself is stored wrapped under an **AndroidKeyStore** key that
 * is not extractable, so lifting the preference file off the device yields a
 * wrapped blob and no way to unwrap it. Deliberately kept separate from
 * `TokenCipher` rather than sharing its key, because the two want opposite
 * behaviour on failure: a token that cannot be decrypted means "log in again",
 * which costs nothing, while a database key that cannot be decrypted means the
 * history is gone. That asymmetry is handled in [passphrase].
 */
@Singleton
class DatabaseKey @Inject constructor(@param:ApplicationContext private val context: Context) {

    /**
     * Returns the passphrase, generating and storing one on first use.
     *
     * Hex rather than raw bytes: the same value has to survive being embedded in
     * an `ATTACH ... KEY '...'` statement during [encryptPlaintextDatabase], and
     * hex is the one encoding that needs no quoting anywhere.
     *
     * @return the passphrase, or `null` if a stored one exists but cannot be
     *   unwrapped — the caller must then treat the database as unreadable rather
     *   than generate a new key, which would strand it.
     */
    @SuppressLint("ApplySharedPref")
    fun passphrase(): ByteArray? {
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        prefs.getString(KEY_WRAPPED, null)?.let { stored ->
            return unwrap(stored)?.toByteArray(Charsets.US_ASCII)
        }

        val fresh = ByteArray(KEY_BYTES).also(SecureRandom()::nextBytes).toHex()
        prefs.edit(commit = true) { putString(KEY_WRAPPED, wrap(fresh)) }
        return fresh.toByteArray(Charsets.US_ASCII)
    }

    /**
     * Forgets the stored passphrase.
     *
     * Only called when the database it belonged to has already been deleted —
     * the two are useless apart, and leaving the key behind would mean the next
     * database is encrypted under a key that once protected something else.
     */
    @SuppressLint("ApplySharedPref")
    fun forget() {
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
            .edit(commit = true) { remove(KEY_WRAPPED) }
    }

    private fun wrap(passphrase: String): String = try {
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, keystoreKey())
        val sealed = cipher.doFinal(passphrase.toByteArray(Charsets.US_ASCII))
        WRAPPED_PREFIX + Base64.encodeToString(cipher.iv + sealed, Base64.NO_WRAP)
    } catch (e: Exception) {
        // An emulator image or an OEM with a broken keystore must not leave the
        // app without a database. Storing the passphrase unwrapped still buys
        // something — the database file alone is no longer readable, which is
        // where a stolen backup or a file-level leak ends up — and it is
        // recorded rather than hidden.
        Log.w(TAG, "keystore unavailable; database key stored unwrapped", e)
        PLAIN_PREFIX + passphrase
    }

    private fun unwrap(stored: String): String? = when {
        stored.startsWith(PLAIN_PREFIX) -> stored.removePrefix(PLAIN_PREFIX)
        stored.startsWith(WRAPPED_PREFIX) -> try {
            val packed = Base64.decode(stored.removePrefix(WRAPPED_PREFIX), Base64.NO_WRAP)
            val cipher = Cipher.getInstance(TRANSFORMATION)
            cipher.init(
                Cipher.DECRYPT_MODE,
                keystoreKey(),
                GCMParameterSpec(TAG_BITS, packed, 0, IV_BYTES),
            )
            String(cipher.doFinal(packed, IV_BYTES, packed.size - IV_BYTES), Charsets.US_ASCII)
        } catch (e: Exception) {
            // The keystore key is gone. This is not supposed to be reachable —
            // the app sets allowBackup="false", so a restore never carries the
            // preference onto a device whose keystore never held the key — but
            // if it happens the honest answer is "unreadable", not a new key.
            Log.e(TAG, "database key could not be unwrapped", e)
            null
        }

        else -> null
    }

    private fun keystoreKey(): SecretKey {
        val keyStore = KeyStore.getInstance(PROVIDER).apply { load(null) }
        (keyStore.getEntry(ALIAS, null) as? KeyStore.SecretKeyEntry)?.let { return it.secretKey }

        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, PROVIDER)
        generator.init(
            KeyGenParameterSpec.Builder(
                ALIAS,
                KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
            )
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                // NOT setUserAuthenticationRequired, for the same reason as the
                // session tokens: a push has to open the database on a locked
                // phone, and there is nobody there to answer a biometric prompt.
                .build(),
        )
        return generator.generateKey()
    }

    private fun ByteArray.toHex(): String {
        val out = StringBuilder(size * 2)
        for (byte in this) out.append(HEX[(byte.toInt() shr 4) and 0xF]).append(HEX[byte.toInt() and 0xF])
        return out.toString()
    }

    private companion object {
        const val TAG = "DatabaseKey"
        const val PREFS = "syncapp.database"
        const val KEY_WRAPPED = "passphrase"
        const val PROVIDER = "AndroidKeyStore"
        const val ALIAS = "syncapp.database.key"
        const val TRANSFORMATION = "AES/GCM/NoPadding"

        /** Distinguishes a wrapped passphrase from the fallback, so both still read. */
        const val WRAPPED_PREFIX = "k1:"
        const val PLAIN_PREFIX = "p1:"

        const val KEY_BYTES = 32
        const val IV_BYTES = 12
        const val TAG_BITS = 128
        const val HEX = "0123456789abcdef"
    }
}
