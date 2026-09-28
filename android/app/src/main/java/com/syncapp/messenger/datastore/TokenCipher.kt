package com.syncapp.messenger.datastore

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import android.util.Log
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Encrypts the session tokens before they touch the disk.
 *
 * A DataStore preferences file is a plain file in app-private storage. That is
 * enough against another app on a healthy device, and nothing at all against a
 * rooted phone, an ADB backup of a debuggable build, or anyone holding the
 * unlocked device — and the values in question are bearer credentials: whoever
 * reads them *is* the user until the session expires, and the server has no
 * message to revoke it with (see SECURITY.md).
 *
 * The key itself is generated in the **AndroidKeyStore** and is not extractable:
 * it lives in the TEE/StrongBox where the hardware has one, so copying the file
 * off the device yields ciphertext and no key. AES-GCM gives authentication as
 * well, so a tampered value fails to decrypt instead of decrypting to garbage.
 *
 * Failure is deliberately soft everywhere: the tokens are a cache of something
 * the user can always re-establish by logging in, so a key that has been
 * invalidated (a restored backup, a changed lock screen) must land the user on
 * the login screen — never crash the app on launch.
 */
@Singleton
class TokenCipher @Inject constructor() {

    /** Returns the encrypted form, or the plaintext unchanged if the keystore is unusable. */
    fun encrypt(plaintext: String): String {
        if (plaintext.isEmpty()) return plaintext
        return try {
            val cipher = Cipher.getInstance(TRANSFORMATION)
            cipher.init(Cipher.ENCRYPT_MODE, key())
            val encrypted = cipher.doFinal(plaintext.toByteArray(Charsets.UTF_8))
            val packed = cipher.iv + encrypted
            PREFIX + Base64.encodeToString(packed, Base64.NO_WRAP)
        } catch (e: Exception) {
            // An emulator image or an OEM with a broken keystore must not make the
            // app unusable. Storing plaintext is the pre-existing behaviour, and it
            // is recorded rather than hidden.
            Log.w(TAG, "keystore unavailable; storing token unencrypted", e)
            plaintext
        }
    }

    /**
     * Reverses [encrypt]. A value without the marker is returned as-is, which is
     * what carries sessions saved before this existed through an app update
     * instead of silently logging everyone out.
     */
    fun decrypt(stored: String): String {
        if (stored.isEmpty() || !stored.startsWith(PREFIX)) return stored
        return try {
            val packed = Base64.decode(stored.removePrefix(PREFIX), Base64.NO_WRAP)
            if (packed.size <= IV_BYTES) return ""
            val cipher = Cipher.getInstance(TRANSFORMATION)
            cipher.init(
                Cipher.DECRYPT_MODE,
                key(),
                GCMParameterSpec(TAG_BITS, packed, 0, IV_BYTES),
            )
            String(cipher.doFinal(packed, IV_BYTES, packed.size - IV_BYTES), Charsets.UTF_8)
        } catch (e: Exception) {
            // The key is gone or the value was tampered with. Report "no token",
            // which routes to the login screen.
            Log.i(TAG, "stored token could not be decrypted: ${e.message}")
            ""
        }
    }

    private fun key(): SecretKey {
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
                // NOT setUserAuthenticationRequired: a push has to be able to wake the
                // app and reconnect on a locked phone, which needs the token without a
                // biometric prompt nobody is there to answer.
                .build(),
        )
        return generator.generateKey()
    }

    private companion object {
        const val TAG = "TokenCipher"
        const val PROVIDER = "AndroidKeyStore"
        const val ALIAS = "syncapp.session.tokens"
        const val TRANSFORMATION = "AES/GCM/NoPadding"

        /** Marks a value as produced by this class, so old plaintext still reads. */
        const val PREFIX = "enc1:"
        const val IV_BYTES = 12
        const val TAG_BITS = 128
    }
}
