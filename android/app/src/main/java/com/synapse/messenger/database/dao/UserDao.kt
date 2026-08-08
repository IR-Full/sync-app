package com.synapse.messenger.database.dao

import androidx.room.Dao
import androidx.room.Insert
import androidx.room.OnConflictStrategy
import androidx.room.Query
import androidx.room.Transaction
import com.synapse.messenger.database.entity.UserEntity
import kotlinx.coroutines.flow.Flow

@Dao
interface UserDao {

    @Query("SELECT * FROM users ORDER BY COALESCE(name, username, userId) COLLATE NOCASE ASC")
    fun observeAll(): Flow<List<UserEntity>>

    @Query("SELECT * FROM users WHERE userId = :userId")
    suspend fun findById(userId: String): UserEntity?

    @Query("SELECT * FROM users WHERE username = :username COLLATE NOCASE LIMIT 1")
    suspend fun findByUsername(username: String): UserEntity?

    @Insert(onConflict = OnConflictStrategy.IGNORE)
    suspend fun insertIgnore(user: UserEntity)

    @Query("UPDATE users SET username = COALESCE(:username, username), name = COALESCE(:name, name), isContact = :isContact, blocked = :blocked, updatedAt = :updatedAt WHERE userId = :userId")
    suspend fun update(
        userId: String,
        username: String?,
        name: String?,
        isContact: Boolean,
        blocked: Boolean,
        updatedAt: Long,
    )

    @Query("DELETE FROM users")
    suspend fun clear()

    /**
     * Merges what we just learned about a person. A username only ever comes from
     * us typing it (the server never sends one back), so it must never be
     * overwritten with null by a later CONTACT_SYNC that knows only the id.
     */
    @Transaction
    suspend fun upsert(
        userId: String,
        username: String? = null,
        name: String? = null,
        isContact: Boolean = false,
        blocked: Boolean = false,
        updatedAt: Long = System.currentTimeMillis(),
    ) {
        val existing = findById(userId)
        if (existing == null) {
            insertIgnore(
                UserEntity(
                    userId = userId,
                    username = username,
                    name = name,
                    isContact = isContact,
                    blocked = blocked,
                    updatedAt = updatedAt,
                ),
            )
            return
        }
        update(
            userId = userId,
            username = username,
            name = name,
            isContact = isContact || existing.isContact,
            blocked = blocked,
            updatedAt = updatedAt,
        )
    }
}
