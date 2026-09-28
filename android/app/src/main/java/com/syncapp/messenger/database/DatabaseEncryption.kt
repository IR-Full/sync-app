package com.syncapp.messenger.database

import android.content.Context
import android.util.Log
import java.io.File
import net.zetetic.database.sqlcipher.SQLiteDatabase

/**
 * Converts a database written before this app encrypted anything.
 *
 * Every installation that predates SQLCipher has a plaintext `syncapp.db` on
 * disk, and it cannot simply be discarded: the outbox lives in it, so dropping
 * the file would throw away messages the user watched leave the compose box.
 * The whole thing is copied into an encrypted database instead, which is what
 * `sqlcipher_export` is for.
 *
 * Called before Room opens, once, on a cold start. The cost is proportional to
 * the history and is paid exactly once per device.
 */
internal object DatabaseEncryption {

    private const val TAG = "DatabaseEncryption"

    /**
     * Encrypts [name] in place if it exists and is not already encrypted.
     *
     * The rename is the commit point: until it happens the original file is
     * untouched, so a process death halfway through leaves the plaintext
     * database intact and the next launch simply starts over.
     */
    fun encryptPlaintextDatabase(context: Context, name: String, passphrase: ByteArray) {
        val original = context.getDatabasePath(name)
        if (!original.exists() || isEncrypted(original, passphrase)) return

        // A leftover from a previous attempt that died before the rename.
        val encrypted = File(original.parentFile, "$name.migrating")
        encrypted.delete()

        Log.i(TAG, "converting plaintext database to SQLCipher")
        val plaintext = SQLiteDatabase.openOrCreateDatabase(original, "", null, null)
        val version: Int
        try {
            plaintext.rawExecSQL(
                "ATTACH DATABASE '${encrypted.absolutePath}' AS encrypted KEY '${passphrase.asKey()}'",
            )
            plaintext.rawExecSQL("SELECT sqlcipher_export('encrypted')")
            plaintext.rawExecSQL("DETACH DATABASE encrypted")
            // sqlcipher_export copies the contents and not the schema version.
            // Carrying it across is what stops Room from re-running migrations
            // that have already been applied.
            version = plaintext.version
        } finally {
            plaintext.close()
        }

        SQLiteDatabase.openOrCreateDatabase(encrypted, String(passphrase, Charsets.US_ASCII), null, null)
            .use { it.version = version }

        // Room's journal and write-ahead log belong to the file being replaced.
        // Left behind, they would be read as companions of the new one.
        listOf("$name-journal", "$name-wal", "$name-shm").forEach {
            File(original.parentFile, it).delete()
        }
        check(original.delete() && encrypted.renameTo(original)) {
            "could not replace $name with its encrypted copy"
        }
        Log.i(TAG, "database converted")
    }

    /**
     * Whether the file already opens under [passphrase].
     *
     * Asked by trying, because a SQLCipher database is indistinguishable from
     * random bytes by design — there is no header to read that would answer
     * this without the key.
     */
    private fun isEncrypted(file: File, passphrase: ByteArray): Boolean = try {
        SQLiteDatabase.openDatabase(
            file.absolutePath,
            String(passphrase, Charsets.US_ASCII),
            null,
            SQLiteDatabase.OPEN_READONLY,
            null,
        ).use { it.version }
        true
    } catch (e: Exception) {
        Log.d(TAG, "database does not open with the current key: ${e.message}")
        false
    }

    /**
     * Renders the passphrase for use inside an `ATTACH ... KEY '...'` clause.
     *
     * Safe to interpolate only because [DatabaseKey] hands out hexadecimal,
     * which contains nothing SQL would treat as punctuation.
     */
    private fun ByteArray.asKey(): String = String(this, Charsets.US_ASCII)
}
