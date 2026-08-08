// GENERATED FILE — do not edit by hand.
// Source: server/proto/synapse/v1/body.proto (regenerate: npm run proto:gen)

export const descriptor = {
  "nested": {
    "synapse": {
      "nested": {
        "v1": {
          "options": {
            "go_package": "github.com/synapse-chat/synapse/internal/wirepb;wirepb"
          },
          "nested": {
            "Hello": {
              "fields": {
                "clientVersion": {
                  "type": "string",
                  "id": 1,
                  "protoName": "client_version"
                },
                "deviceId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "device_id"
                },
                "platform": {
                  "type": "string",
                  "id": 3
                },
                "caps": {
                  "type": "uint32",
                  "id": 4
                },
                "resumeToken": {
                  "type": "string",
                  "id": 5,
                  "protoName": "resume_token"
                }
              }
            },
            "Welcome": {
              "fields": {
                "serverVersion": {
                  "type": "string",
                  "id": 1,
                  "protoName": "server_version"
                },
                "sessionId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "session_id"
                },
                "caps": {
                  "type": "uint32",
                  "id": 3
                },
                "heartbeatMs": {
                  "type": "int32",
                  "id": 4,
                  "protoName": "heartbeat_ms"
                },
                "maxInflight": {
                  "type": "int32",
                  "id": 5,
                  "protoName": "max_inflight"
                },
                "resumeSupported": {
                  "type": "bool",
                  "id": 6,
                  "protoName": "resume_supported"
                }
              }
            },
            "Auth": {
              "fields": {
                "token": {
                  "type": "string",
                  "id": 1
                },
                "username": {
                  "type": "string",
                  "id": 2
                },
                "password": {
                  "type": "string",
                  "id": 3
                },
                "register": {
                  "type": "bool",
                  "id": 4
                }
              }
            },
            "AuthOK": {
              "fields": {
                "userId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "user_id"
                },
                "deviceId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "device_id"
                },
                "sessionId": {
                  "type": "string",
                  "id": 3,
                  "protoName": "session_id"
                },
                "token": {
                  "type": "string",
                  "id": 4
                },
                "resumeToken": {
                  "type": "string",
                  "id": 5,
                  "protoName": "resume_token"
                }
              }
            },
            "Send": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "dedupKey": {
                  "type": "string",
                  "id": 2,
                  "protoName": "dedup_key"
                },
                "text": {
                  "type": "string",
                  "id": 3
                },
                "mediaRef": {
                  "type": "string",
                  "id": 4,
                  "protoName": "media_ref"
                },
                "replyTo": {
                  "type": "string",
                  "id": 5,
                  "protoName": "reply_to"
                },
                "attachment": {
                  "type": "Attachment",
                  "id": 6
                },
                "ttlSeconds": {
                  "type": "int32",
                  "id": 7,
                  "protoName": "ttl_seconds"
                }
              }
            },
            "Attachment": {
              "fields": {
                "kind": {
                  "type": "string",
                  "id": 1
                },
                "mediaRef": {
                  "type": "string",
                  "id": 2,
                  "protoName": "media_ref"
                },
                "filename": {
                  "type": "string",
                  "id": 3
                },
                "mime": {
                  "type": "string",
                  "id": 4
                },
                "size": {
                  "type": "int64",
                  "id": 5
                },
                "durationMs": {
                  "type": "int64",
                  "id": 6,
                  "protoName": "duration_ms"
                },
                "waveform": {
                  "rule": "repeated",
                  "type": "int32",
                  "id": 7
                },
                "width": {
                  "type": "int32",
                  "id": 8
                },
                "height": {
                  "type": "int32",
                  "id": 9
                },
                "thumbRef": {
                  "type": "string",
                  "id": 10,
                  "protoName": "thumb_ref"
                }
              }
            },
            "SendAck": {
              "fields": {
                "dedupKey": {
                  "type": "string",
                  "id": 1,
                  "protoName": "dedup_key"
                },
                "messageId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "message_id"
                },
                "chatId": {
                  "type": "string",
                  "id": 3,
                  "protoName": "chat_id"
                },
                "chatSeq": {
                  "type": "uint64",
                  "id": 4,
                  "protoName": "chat_seq"
                },
                "timestamp": {
                  "type": "int64",
                  "id": 5
                },
                "duplicate": {
                  "type": "bool",
                  "id": 6
                }
              }
            },
            "NewMessage": {
              "fields": {
                "messageId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "message_id"
                },
                "chatId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "chat_id"
                },
                "senderId": {
                  "type": "string",
                  "id": 3,
                  "protoName": "sender_id"
                },
                "chatSeq": {
                  "type": "uint64",
                  "id": 4,
                  "protoName": "chat_seq"
                },
                "text": {
                  "type": "string",
                  "id": 5
                },
                "mediaRef": {
                  "type": "string",
                  "id": 6,
                  "protoName": "media_ref"
                },
                "replyTo": {
                  "type": "string",
                  "id": 7,
                  "protoName": "reply_to"
                },
                "edited": {
                  "type": "bool",
                  "id": 8
                },
                "deleted": {
                  "type": "bool",
                  "id": 9
                },
                "timestamp": {
                  "type": "int64",
                  "id": 10
                },
                "attachment": {
                  "type": "Attachment",
                  "id": 11
                },
                "threadRoot": {
                  "type": "string",
                  "id": 12,
                  "protoName": "thread_root"
                },
                "replyCount": {
                  "type": "int32",
                  "id": 13,
                  "protoName": "reply_count"
                },
                "forward": {
                  "type": "ForwardOrigin",
                  "id": 14
                },
                "expiresAt": {
                  "type": "int64",
                  "id": 15,
                  "protoName": "expires_at"
                }
              }
            },
            "Thread": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "rootId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "root_id"
                },
                "afterSeq": {
                  "type": "uint64",
                  "id": 3,
                  "protoName": "after_seq"
                },
                "limit": {
                  "type": "int32",
                  "id": 4
                }
              }
            },
            "ThreadOK": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "rootId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "root_id"
                },
                "nextAfter": {
                  "type": "uint64",
                  "id": 3,
                  "protoName": "next_after"
                },
                "done": {
                  "type": "bool",
                  "id": 4
                }
              }
            },
            "Read": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "upToMessageId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "up_to_message_id"
                },
                "upToChatSeq": {
                  "type": "uint64",
                  "id": 3,
                  "protoName": "up_to_chat_seq"
                }
              }
            },
            "ReadUpdate": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "userId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "user_id"
                },
                "upToChatSeq": {
                  "type": "uint64",
                  "id": 3,
                  "protoName": "up_to_chat_seq"
                }
              }
            },
            "Typing": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "userId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "user_id"
                },
                "active": {
                  "type": "bool",
                  "id": 3
                }
              }
            },
            "React": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "messageId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "message_id"
                },
                "emoji": {
                  "type": "string",
                  "id": 3
                }
              }
            },
            "ReactUpdate": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "messageId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "message_id"
                },
                "userId": {
                  "type": "string",
                  "id": 3,
                  "protoName": "user_id"
                },
                "emoji": {
                  "type": "string",
                  "id": 4
                },
                "added": {
                  "type": "bool",
                  "id": 5
                },
                "counts": {
                  "keyType": "string",
                  "type": "int32",
                  "id": 6
                }
              }
            },
            "Presence": {
              "fields": {
                "userId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "user_id"
                },
                "online": {
                  "type": "bool",
                  "id": 2
                },
                "lastSeenMs": {
                  "type": "int64",
                  "id": 3,
                  "protoName": "last_seen_ms"
                }
              }
            },
            "Edit": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "messageId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "message_id"
                },
                "text": {
                  "type": "string",
                  "id": 3
                }
              }
            },
            "Delete": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "messageId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "message_id"
                },
                "forAll": {
                  "type": "bool",
                  "id": 3,
                  "protoName": "for_all"
                }
              }
            },
            "History": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "beforeSeq": {
                  "type": "uint64",
                  "id": 2,
                  "protoName": "before_seq"
                },
                "limit": {
                  "type": "int32",
                  "id": 3
                }
              }
            },
            "HistoryOK": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "nextBefore": {
                  "type": "uint64",
                  "id": 2,
                  "protoName": "next_before"
                },
                "done": {
                  "type": "bool",
                  "id": 3
                }
              }
            },
            "Resume": {
              "fields": {
                "resumeToken": {
                  "type": "string",
                  "id": 1,
                  "protoName": "resume_token"
                },
                "lastAckSeq": {
                  "type": "uint64",
                  "id": 2,
                  "protoName": "last_ack_seq"
                }
              }
            },
            "ResumeOK": {
              "fields": {
                "sessionId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "session_id"
                },
                "fromSeq": {
                  "type": "uint64",
                  "id": 2,
                  "protoName": "from_seq"
                }
              }
            },
            "Error": {
              "fields": {
                "code": {
                  "type": "uint32",
                  "id": 1
                },
                "message": {
                  "type": "string",
                  "id": 2
                },
                "retryAfterMs": {
                  "type": "int32",
                  "id": 3,
                  "protoName": "retry_after_ms"
                }
              }
            },
            "MediaInit": {
              "fields": {
                "filename": {
                  "type": "string",
                  "id": 1
                },
                "contentType": {
                  "type": "string",
                  "id": 2,
                  "protoName": "content_type"
                },
                "size": {
                  "type": "int64",
                  "id": 3
                }
              }
            },
            "MediaTicket": {
              "fields": {
                "mediaRef": {
                  "type": "string",
                  "id": 1,
                  "protoName": "media_ref"
                },
                "uploadUrl": {
                  "type": "string",
                  "id": 2,
                  "protoName": "upload_url"
                },
                "expiresAt": {
                  "type": "int64",
                  "id": 3,
                  "protoName": "expires_at"
                }
              }
            },
            "MediaFetch": {
              "fields": {
                "mediaRef": {
                  "type": "string",
                  "id": 1,
                  "protoName": "media_ref"
                }
              }
            },
            "MediaURL": {
              "fields": {
                "mediaRef": {
                  "type": "string",
                  "id": 1,
                  "protoName": "media_ref"
                },
                "downloadUrl": {
                  "type": "string",
                  "id": 2,
                  "protoName": "download_url"
                },
                "expiresAt": {
                  "type": "int64",
                  "id": 3,
                  "protoName": "expires_at"
                }
              }
            },
            "Search": {
              "fields": {
                "query": {
                  "type": "string",
                  "id": 1
                },
                "limit": {
                  "type": "int32",
                  "id": 2
                }
              }
            },
            "SearchHit": {
              "fields": {
                "messageId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "message_id"
                },
                "chatId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "chat_id"
                },
                "senderId": {
                  "type": "string",
                  "id": 3,
                  "protoName": "sender_id"
                },
                "seq": {
                  "type": "uint64",
                  "id": 4
                },
                "text": {
                  "type": "string",
                  "id": 5
                }
              }
            },
            "SearchResults": {
              "fields": {
                "query": {
                  "type": "string",
                  "id": 1
                },
                "hits": {
                  "rule": "repeated",
                  "type": "SearchHit",
                  "id": 2
                }
              }
            },
            "KeyPublish": {
              "fields": {
                "identityKey": {
                  "type": "string",
                  "id": 1,
                  "protoName": "identity_key"
                },
                "signingKey": {
                  "type": "string",
                  "id": 2,
                  "protoName": "signing_key"
                },
                "signedPrekey": {
                  "type": "string",
                  "id": 3,
                  "protoName": "signed_prekey"
                },
                "signedPrekeySig": {
                  "type": "string",
                  "id": 4,
                  "protoName": "signed_prekey_sig"
                },
                "prekeys": {
                  "rule": "repeated",
                  "type": "string",
                  "id": 5
                }
              }
            },
            "KeyFetch": {
              "fields": {
                "userId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "user_id"
                },
                "deviceId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "device_id"
                }
              }
            },
            "KeyBundle": {
              "fields": {
                "userId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "user_id"
                },
                "deviceId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "device_id"
                },
                "identityKey": {
                  "type": "string",
                  "id": 3,
                  "protoName": "identity_key"
                },
                "signingKey": {
                  "type": "string",
                  "id": 4,
                  "protoName": "signing_key"
                },
                "signedPrekey": {
                  "type": "string",
                  "id": 5,
                  "protoName": "signed_prekey"
                },
                "signedPrekeySig": {
                  "type": "string",
                  "id": 6,
                  "protoName": "signed_prekey_sig"
                },
                "oneTimePrekey": {
                  "type": "string",
                  "id": 7,
                  "protoName": "one_time_prekey"
                }
              }
            },
            "KeyBundles": {
              "fields": {
                "userId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "user_id"
                },
                "bundles": {
                  "rule": "repeated",
                  "type": "KeyBundle",
                  "id": 2
                }
              }
            },
            "SecretMsg": {
              "fields": {
                "toUserId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "to_user_id"
                },
                "toDeviceId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "to_device_id"
                },
                "fromUserId": {
                  "type": "string",
                  "id": 3,
                  "protoName": "from_user_id"
                },
                "fromDeviceId": {
                  "type": "string",
                  "id": 4,
                  "protoName": "from_device_id"
                },
                "ratchetHeader": {
                  "type": "string",
                  "id": 5,
                  "protoName": "ratchet_header"
                },
                "ciphertext": {
                  "type": "string",
                  "id": 6
                }
              }
            },
            "ChatExport": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                }
              }
            },
            "ChatMember": {
              "fields": {
                "userId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "user_id"
                },
                "role": {
                  "type": "string",
                  "id": 2
                },
                "joinedAt": {
                  "type": "int64",
                  "id": 3,
                  "protoName": "joined_at"
                }
              }
            },
            "ChatExportResult": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "type": {
                  "type": "string",
                  "id": 2
                },
                "title": {
                  "type": "string",
                  "id": 3
                },
                "ownerId": {
                  "type": "string",
                  "id": 4,
                  "protoName": "owner_id"
                },
                "members": {
                  "rule": "repeated",
                  "type": "ChatMember",
                  "id": 5
                },
                "messages": {
                  "rule": "repeated",
                  "type": "NewMessage",
                  "id": 6
                },
                "done": {
                  "type": "bool",
                  "id": 7
                }
              }
            },
            "CallInvite": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "kind": {
                  "type": "string",
                  "id": 2
                }
              }
            },
            "CallAction": {
              "fields": {
                "callId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "call_id"
                }
              }
            },
            "CallParticipant": {
              "fields": {
                "userId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "user_id"
                },
                "deviceId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "device_id"
                },
                "state": {
                  "type": "string",
                  "id": 3
                }
              }
            },
            "CallState": {
              "fields": {
                "callId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "call_id"
                },
                "chatId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "chat_id"
                },
                "initiatorId": {
                  "type": "string",
                  "id": 3,
                  "protoName": "initiator_id"
                },
                "kind": {
                  "type": "string",
                  "id": 4
                },
                "state": {
                  "type": "string",
                  "id": 5
                },
                "participants": {
                  "rule": "repeated",
                  "type": "CallParticipant",
                  "id": 6
                }
              }
            },
            "CallSignal": {
              "fields": {
                "callId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "call_id"
                },
                "toUserId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "to_user_id"
                },
                "toDeviceId": {
                  "type": "string",
                  "id": 3,
                  "protoName": "to_device_id"
                },
                "fromUserId": {
                  "type": "string",
                  "id": 4,
                  "protoName": "from_user_id"
                },
                "fromDeviceId": {
                  "type": "string",
                  "id": 5,
                  "protoName": "from_device_id"
                },
                "signalType": {
                  "type": "string",
                  "id": 6,
                  "protoName": "signal_type"
                },
                "payload": {
                  "type": "string",
                  "id": 7
                }
              }
            },
            "PollCreate": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "question": {
                  "type": "string",
                  "id": 2
                },
                "options": {
                  "rule": "repeated",
                  "type": "string",
                  "id": 3
                },
                "multiChoice": {
                  "type": "bool",
                  "id": 4,
                  "protoName": "multi_choice"
                },
                "anonymous": {
                  "type": "bool",
                  "id": 5
                }
              }
            },
            "PollVote": {
              "fields": {
                "pollId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "poll_id"
                },
                "option": {
                  "type": "int32",
                  "id": 2
                }
              }
            },
            "PollClose": {
              "fields": {
                "pollId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "poll_id"
                }
              }
            },
            "PollOption": {
              "fields": {
                "index": {
                  "type": "int32",
                  "id": 1
                },
                "text": {
                  "type": "string",
                  "id": 2
                },
                "votes": {
                  "type": "int32",
                  "id": 3
                }
              }
            },
            "PollState": {
              "fields": {
                "pollId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "poll_id"
                },
                "chatId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "chat_id"
                },
                "messageId": {
                  "type": "string",
                  "id": 3,
                  "protoName": "message_id"
                },
                "question": {
                  "type": "string",
                  "id": 4
                },
                "options": {
                  "rule": "repeated",
                  "type": "PollOption",
                  "id": 5
                },
                "totalVotes": {
                  "type": "int32",
                  "id": 6,
                  "protoName": "total_votes"
                },
                "multiChoice": {
                  "type": "bool",
                  "id": 7,
                  "protoName": "multi_choice"
                },
                "anonymous": {
                  "type": "bool",
                  "id": 8
                },
                "closed": {
                  "type": "bool",
                  "id": 9
                },
                "myVotes": {
                  "rule": "repeated",
                  "type": "int32",
                  "id": 10,
                  "protoName": "my_votes"
                }
              }
            },
            "ContactAdd": {
              "fields": {
                "target": {
                  "type": "string",
                  "id": 1
                },
                "name": {
                  "type": "string",
                  "id": 2
                }
              }
            },
            "ContactRemove": {
              "fields": {
                "target": {
                  "type": "string",
                  "id": 1
                }
              }
            },
            "ContactSync": {
              "fields": {
                "since": {
                  "type": "int64",
                  "id": 1
                }
              }
            },
            "Contact": {
              "fields": {
                "userId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "user_id"
                },
                "name": {
                  "type": "string",
                  "id": 2
                },
                "blocked": {
                  "type": "bool",
                  "id": 3
                },
                "updatedAt": {
                  "type": "int64",
                  "id": 4,
                  "protoName": "updated_at"
                }
              }
            },
            "ContactList": {
              "fields": {
                "contacts": {
                  "rule": "repeated",
                  "type": "Contact",
                  "id": 1
                },
                "cursor": {
                  "type": "int64",
                  "id": 2
                }
              }
            },
            "Block": {
              "fields": {
                "target": {
                  "type": "string",
                  "id": 1
                },
                "blocked": {
                  "type": "bool",
                  "id": 2
                }
              }
            },
            "ForwardOrigin": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "messageId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "message_id"
                },
                "senderId": {
                  "type": "string",
                  "id": 3,
                  "protoName": "sender_id"
                }
              }
            },
            "Forward": {
              "fields": {
                "fromChatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "from_chat_id"
                },
                "messageId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "message_id"
                },
                "toChatId": {
                  "type": "string",
                  "id": 3,
                  "protoName": "to_chat_id"
                },
                "dedupKey": {
                  "type": "string",
                  "id": 4,
                  "protoName": "dedup_key"
                }
              }
            },
            "Schedule": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "text": {
                  "type": "string",
                  "id": 2
                },
                "mediaRef": {
                  "type": "string",
                  "id": 3,
                  "protoName": "media_ref"
                },
                "attachment": {
                  "type": "Attachment",
                  "id": 4
                },
                "replyTo": {
                  "type": "string",
                  "id": 5,
                  "protoName": "reply_to"
                },
                "ttlSeconds": {
                  "type": "int32",
                  "id": 6,
                  "protoName": "ttl_seconds"
                },
                "sendAt": {
                  "type": "int64",
                  "id": 7,
                  "protoName": "send_at"
                }
              }
            },
            "ScheduleList": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                }
              }
            },
            "ScheduleCancel": {
              "fields": {
                "id": {
                  "type": "string",
                  "id": 1
                }
              }
            },
            "ScheduledItem": {
              "fields": {
                "id": {
                  "type": "string",
                  "id": 1
                },
                "chatId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "chat_id"
                },
                "text": {
                  "type": "string",
                  "id": 3
                },
                "sendAt": {
                  "type": "int64",
                  "id": 4,
                  "protoName": "send_at"
                }
              }
            },
            "Scheduled": {
              "fields": {
                "items": {
                  "rule": "repeated",
                  "type": "ScheduledItem",
                  "id": 1
                }
              }
            },
            "Pin": {
              "fields": {
                "messageId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "message_id"
                },
                "pinnedBy": {
                  "type": "string",
                  "id": 2,
                  "protoName": "pinned_by"
                },
                "pinnedAt": {
                  "type": "int64",
                  "id": 3,
                  "protoName": "pinned_at"
                }
              }
            },
            "PinAction": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "messageId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "message_id"
                }
              }
            },
            "Pinned": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "pins": {
                  "rule": "repeated",
                  "type": "Pin",
                  "id": 2
                }
              }
            },
            "Draft": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "text": {
                  "type": "string",
                  "id": 2
                },
                "replyTo": {
                  "type": "string",
                  "id": 3,
                  "protoName": "reply_to"
                }
              }
            },
            "DraftSync": {
              "fields": {
                "since": {
                  "type": "int64",
                  "id": 1
                }
              }
            },
            "DraftItem": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "text": {
                  "type": "string",
                  "id": 2
                },
                "replyTo": {
                  "type": "string",
                  "id": 3,
                  "protoName": "reply_to"
                },
                "updatedAt": {
                  "type": "int64",
                  "id": 4,
                  "protoName": "updated_at"
                }
              }
            },
            "Drafts": {
              "fields": {
                "drafts": {
                  "rule": "repeated",
                  "type": "DraftItem",
                  "id": 1
                },
                "cursor": {
                  "type": "int64",
                  "id": 2
                }
              }
            },
            "SetUsername": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "username": {
                  "type": "string",
                  "id": 2
                }
              }
            },
            "InviteCreate": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "expiresAt": {
                  "type": "int64",
                  "id": 2,
                  "protoName": "expires_at"
                },
                "maxUses": {
                  "type": "int32",
                  "id": 3,
                  "protoName": "max_uses"
                }
              }
            },
            "InviteRevoke": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "code": {
                  "type": "string",
                  "id": 2
                }
              }
            },
            "InviteList": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                }
              }
            },
            "Join": {
              "fields": {
                "code": {
                  "type": "string",
                  "id": 1
                },
                "handle": {
                  "type": "string",
                  "id": 2
                }
              }
            },
            "SetRole": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "userId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "user_id"
                },
                "role": {
                  "type": "string",
                  "id": 3
                }
              }
            },
            "InviteLink": {
              "fields": {
                "code": {
                  "type": "string",
                  "id": 1
                },
                "chatId": {
                  "type": "string",
                  "id": 2,
                  "protoName": "chat_id"
                },
                "expiresAt": {
                  "type": "int64",
                  "id": 3,
                  "protoName": "expires_at"
                },
                "maxUses": {
                  "type": "int32",
                  "id": 4,
                  "protoName": "max_uses"
                },
                "uses": {
                  "type": "int32",
                  "id": 5
                }
              }
            },
            "Invites": {
              "fields": {
                "links": {
                  "rule": "repeated",
                  "type": "InviteLink",
                  "id": 1
                },
                "joinedChat": {
                  "type": "string",
                  "id": 2,
                  "protoName": "joined_chat"
                }
              }
            },
            "FanoutShard": {
              "fields": {
                "body": {
                  "type": "NewMessage",
                  "id": 1
                },
                "members": {
                  "rule": "repeated",
                  "type": "string",
                  "id": 2
                }
              }
            },
            "ChatCreate": {
              "fields": {
                "type": {
                  "type": "string",
                  "id": 1
                },
                "title": {
                  "type": "string",
                  "id": 2
                },
                "members": {
                  "rule": "repeated",
                  "type": "string",
                  "id": 3
                }
              }
            },
            "ChatInfo": {
              "fields": {
                "chatId": {
                  "type": "string",
                  "id": 1,
                  "protoName": "chat_id"
                },
                "type": {
                  "type": "string",
                  "id": 2
                },
                "title": {
                  "type": "string",
                  "id": 3
                },
                "ownerId": {
                  "type": "string",
                  "id": 4,
                  "protoName": "owner_id"
                }
              }
            },
            "PushToken": {
              "fields": {
                "token": {
                  "type": "string",
                  "id": 1
                }
              }
            }
          }
        }
      }
    }
  }
} as const

export default descriptor
