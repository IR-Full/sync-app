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

    @Query("SELECT * FROM users WHERE userId = :userId")
    fun observeById(userId: String): Flow<UserEntity?>

    @Query("SELECT * FROM users WHERE username = :username COLLATE NOCASE LIMIT 1")
    suspend fun findByUsername(username: String): UserEntity?

    @Insert(onConflict = OnConflictStrategy.IGNORE)
    suspend fun insertIgnore(user: UserEntity)

    /**
     * COALESCE everywhere: each writer knows only part of a person. A contact sync
     * carries our private label and no handle, a profile fetch carries their public
     * name and no label, and a chat-list peer carries neither — none of them may
     * blank out what another one learned.
     */
    @Query(
        """
        UPDATE users SET
            username = COALESCE(:username, username),
            name = COALESCE(:name, name),
            displayName = COALESCE(:displayName, displayName),
            avatarRef = COALESCE(:avatarRef, avatarRef),
            isContact = :isContact,
            blocked = :blocked,
            updatedAt = :updatedAt
        WHERE userId = :userId
        """,
    )
    suspend fun update(
        userId: String,
        username: String?,
        name: String?,
        displayName: String?,
        avatarRef: String?,
        isContact: Boolean,
        blocked: Boolean,
        updatedAt: Long,
    )

    /** Clearing an avatar is explicit, because COALESCE cannot express "remove". */
    @Query("UPDATE users SET avatarRef = NULL WHERE userId = :userId")
    suspend fun clearAvatar(userId: String)

    /** Peers we can name only by id — the profiles worth fetching. */
    @Query("SELECT userId FROM users WHERE displayName IS NULL AND username IS NULL")
    suspend fun idsWithoutProfile(): List<String>

    @Query("DELETE FROM users")
    suspend fun clear()

    /** Merges what we just learned about a person, from whichever source knew it. */
    @Transaction
    suspend fun upsert(
        userId: String,
        username: String? = null,
        name: String? = null,
        displayName: String? = null,
        avatarRef: String? = null,
        isContact: Boolean = false,
        blocked: Boolean = false,
        updatedAt: Long = System.currentTimeMillis(),
    ) {
        if (userId.isEmpty()) return
        val existing = findById(userId)
        if (existing == null) {
            insertIgnore(
                UserEntity(
                    userId = userId,
                    username = username,
                    name = name,
                    displayName = displayName,
                    avatarRef = avatarRef,
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
            displayName = displayName,
            avatarRef = avatarRef,
            isContact = isContact || existing.isContact,
            blocked = existing.blocked || blocked,
            updatedAt = updatedAt,
        )
    }
}
