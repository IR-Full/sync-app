package com.syncapp.messenger.data.sync

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import androidx.datastore.preferences.core.Preferences
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import com.syncapp.messenger.data.SessionHolder
import com.syncapp.messenger.database.SyncAppDatabase
import com.syncapp.messenger.datastore.SessionStore
import com.syncapp.messenger.datastore.TokenCipher
import com.syncapp.messenger.network.GatewaySession
import java.io.File
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking

/**
 * A real database and a real session store, wired the way the app wires them.
 *
 * Deliberately not mocks. Everything interesting in the ingest path is expressed
 * as SQL — `upsertKnown` is an INSERT OR IGNORE plus a COALESCE update,
 * `advanceReadCursor` is a MAX, `replaceLocal` is a delete and an insert inside
 * one transaction — so a fake DAO would assert that the Kotlin called the method
 * it obviously calls, and prove nothing about the query that actually runs.
 *
 * Room's in-memory builder gives a genuine SQLite under Robolectric, which is
 * what makes these assertions worth making.
 */
class IngestFixture private constructor(
    val db: SyncAppDatabase,
    val sessionHolder: SessionHolder,
    private val scope: CoroutineScope,
    private val dataStoreFile: File,
) {
    val ingestor = MessageIngestor(db, sessionHolder)

    val chats get() = db.chatDao()
    val messages get() = db.messageDao()
    val outbox get() = db.outboxDao()
    val receipts get() = db.readReceiptDao()
    val users get() = db.userDao()

    fun close() {
        db.close()
        dataStoreFile.delete()
    }

    companion object {
        /**
         * Builds the fixture with [selfUserId] signed in.
         *
         * The session is written through the real SessionStore rather than stubbed,
         * because SessionHolder reads it through a `stateIn` that has to have
         * emitted before `currentUserId` is anything but "" — and an ingest that
         * ran with an empty self id would file every message as somebody else's.
         */
        fun create(selfUserId: String = "self"): IngestFixture {
            val context = ApplicationProvider.getApplicationContext<Context>()
            val db = Room.inMemoryDatabaseBuilder(context, SyncAppDatabase::class.java)
                .allowMainThreadQueries()
                .build()

            val file = File.createTempFile("session-test", ".preferences_pb").apply { delete() }
            val store: DataStore<Preferences> =
                PreferenceDataStoreFactory.create(produceFile = { file })
            val sessionStore = SessionStore(store, TokenCipher())

            val scope = CoroutineScope(Dispatchers.Unconfined)
            runBlocking {
                sessionStore.save(
                    GatewaySession(
                        userId = selfUserId,
                        deviceId = "device-1",
                        sessionId = "session-1",
                        token = "token",
                        resumeToken = "resume",
                        username = "alice",
                        displayName = "Alice",
                        avatarRef = "",
                    ),
                    username = "alice",
                )
                // Wait for the store to actually hold it, so the holder's StateFlow
                // has a real id rather than its initial "".
                sessionStore.session.first { it?.userId == selfUserId }
            }

            val holder = SessionHolder(sessionStore, scope)
            runBlocking { holder.userId.first { it == selfUserId } }

            return IngestFixture(db, holder, scope, file)
        }
    }
}
