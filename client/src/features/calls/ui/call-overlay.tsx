'use client'

import { useMemo } from 'react'

import { isIncomingRing, peerKey, useCallStore } from '@/entities/call'
import { useSessionStore } from '@/entities/session'
import { labelForUser, useUserDirectory } from '@/entities/user'
import { useTranslate } from '@/shared/i18n'
import { cn } from '@/shared/lib/cn'
import { Button, ErrorNote } from '@/shared/ui'

import { useCallActions } from '../model/use-calls'
import { VideoTile } from './video-tile'

function RoundButton({
  onClick,
  label,
  active,
  tone = 'neutral',
  children,
}: {
  onClick: () => void
  label: string
  active?: boolean
  tone?: 'neutral' | 'danger'
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      title={label}
      aria-pressed={active}
      className={cn(
        'flex size-12 items-center justify-center rounded-full transition-colors',
        tone === 'danger'
          ? 'bg-danger text-white hover:opacity-90'
          : active
            ? 'bg-white text-black'
            : 'bg-white/15 text-white hover:bg-white/25',
      )}
    >
      {children}
    </button>
  )
}

/**
 * The call surface: an incoming ring, or the active room.
 *
 * Rendered once at the app root rather than inside a chat, because a call
 * outlives navigation — answering must not depend on still standing in the chat
 * the ring came from.
 */
export function CallOverlay() {
  const t = useTranslate()
  const { acceptCall, declineCall, hangUp } = useCallActions()

  const room = useCallStore((state) => state.room)
  const joined = useCallStore((state) => state.joined)
  const localStream = useCallStore((state) => state.localStream)
  const remoteStreams = useCallStore((state) => state.remoteStreams)
  const micMuted = useCallStore((state) => state.micMuted)
  const cameraOff = useCallStore((state) => state.cameraOff)
  const error = useCallStore((state) => state.error)
  const setMicMuted = useCallStore((state) => state.setMicMuted)
  const setCameraOff = useCallStore((state) => state.setCameraOff)

  const selfId = useSessionStore((state) => state.session?.userId ?? '')
  const directory = useUserDirectory((state) => state.users)

  const ringing = isIncomingRing(room, selfId, joined)

  const remotes = useMemo(() => {
    if (!room) return []
    return room.participants
      .filter((participant) => participant.state === 'joined' && participant.userId !== selfId)
      .map((participant) => ({
        key: peerKey(participant.userId, participant.deviceId),
        label: labelForUser(directory[participant.userId], participant.userId),
      }))
  }, [room, selfId, directory])

  // A failure before (or instead of) a room still has to be told to the user;
  // without this the call button would just silently do nothing.
  if (!room || room.state === 'ended') {
    if (!error) return null
    return (
      <div
        role="alert"
        className="border-line bg-surface-raised fixed inset-x-0 top-4 z-50 mx-auto w-[min(24rem,calc(100vw-2rem))] rounded-2xl border p-3 shadow-xl"
      >
        <div className="flex items-start gap-3">
          <p className="text-danger min-w-0 flex-1 text-sm">
            {error === 'permission-denied'
              ? t('call.noPermission')
              : error === 'no-device'
                ? t('call.noDevice')
                : t('call.failed')}
          </p>
          <button
            type="button"
            onClick={() => useCallStore.getState().setError(null)}
            className="text-ink-faint hover:bg-surface-hover hover:text-ink shrink-0 rounded-lg p-1"
            aria-label={t('common.close')}
          >
            <svg
              viewBox="0 0 20 20"
              className="size-4"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.6"
            >
              <path d="M5 5l10 10M15 5L5 15" strokeLinecap="round" />
            </svg>
          </button>
        </div>
      </div>
    )
  }

  // --- incoming ring
  if (ringing) {
    return (
      <div className="border-line bg-surface-raised fixed inset-x-0 top-4 z-50 mx-auto w-[min(24rem,calc(100vw-2rem))] rounded-2xl border p-4 shadow-xl">
        <p className="text-ink text-sm font-semibold">
          {labelForUser(directory[room.initiatorId], room.initiatorId)}
        </p>
        <p className="text-ink-muted mt-0.5 text-xs">
          {room.kind === 'video' ? t('call.incomingVideo') : t('call.incomingAudio')}
        </p>
        {error && <ErrorNote className="mt-2">{error}</ErrorNote>}
        <div className="mt-3 flex gap-2">
          <Button className="flex-1" onClick={() => void acceptCall()}>
            {t('call.accept')}
          </Button>
          <Button variant="danger" className="flex-1" onClick={declineCall}>
            {t('call.decline')}
          </Button>
        </div>
      </div>
    )
  }

  // Everything below is the in-call surface. It appears as soon as there is a
  // room — not once media is flowing — so the caller sees an outgoing-call
  // screen (with a way to hang up) while the permission prompt is still open,
  // instead of a frozen chat and a room nobody can cancel.
  return (
    <div className="fixed inset-0 z-50 flex flex-col bg-black/95">
      <header className="flex items-center justify-between px-4 py-3 text-white">
        <div className="min-w-0">
          <p className="truncate text-sm font-medium">
            {remotes.length === 1
              ? remotes[0].label
              : t('call.participants', { count: remotes.length + 1 })}
          </p>
          <p className="text-xs text-white/60">
            {!joined
              ? t('call.connecting')
              : room.state === 'ringing'
                ? t('call.ringing')
                : t('call.connected')}
          </p>
        </div>
      </header>

      <div
        className={cn(
          'grid min-h-0 flex-1 gap-2 px-3',
          remotes.length <= 1 ? 'grid-cols-1' : 'grid-cols-2',
        )}
      >
        {remotes.length === 0 ? (
          <div className="flex items-center justify-center text-sm text-white/60">
            {t('call.waiting')}
          </div>
        ) : (
          remotes.map((remote) => (
            <VideoTile
              key={remote.key}
              stream={remoteStreams[remote.key] ?? null}
              label={remote.label}
              audioOnly={room.kind === 'audio'}
            />
          ))
        )}
      </div>

      <div className="flex items-center justify-center gap-3 px-4 py-4">
        <VideoTile
          stream={localStream}
          label={t('chats.you')}
          muted
          mirrored
          audioOnly={room.kind === 'audio'}
          className="h-20 w-28 shrink-0"
        />

        <RoundButton
          onClick={() => setMicMuted(!micMuted)}
          label={micMuted ? t('call.unmute') : t('call.mute')}
          active={micMuted}
        >
          {micMuted ? (
            <svg
              viewBox="0 0 24 24"
              className="size-5"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.8"
            >
              <path d="M4 4l16 16" strokeLinecap="round" />
              <path
                d="M9 9v3a3 3 0 004.5 2.6M15 12V6a3 3 0 00-5.9-.7M18 11a6 6 0 01-1 3.3M12 19v2"
                strokeLinecap="round"
              />
            </svg>
          ) : (
            <svg
              viewBox="0 0 24 24"
              className="size-5"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.8"
            >
              <path d="M12 15a3 3 0 003-3V6a3 3 0 10-6 0v6a3 3 0 003 3z" />
              <path d="M18 11a6 6 0 01-12 0M12 17v4" strokeLinecap="round" />
            </svg>
          )}
        </RoundButton>

        {room.kind === 'video' && (
          <RoundButton
            onClick={() => setCameraOff(!cameraOff)}
            label={cameraOff ? t('call.cameraOn') : t('call.cameraOff')}
            active={cameraOff}
          >
            <svg
              viewBox="0 0 24 24"
              className="size-5"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.8"
            >
              <rect x="3" y="6" width="12" height="12" rx="2" />
              <path d="M15 10.5l6-3v9l-6-3" strokeLinejoin="round" />
              {cameraOff && <path d="M4 4l16 16" strokeLinecap="round" />}
            </svg>
          </RoundButton>
        )}

        <RoundButton onClick={hangUp} label={t('call.hangUp')} tone="danger">
          <svg viewBox="0 0 24 24" className="size-5" fill="currentColor">
            <path
              d="M12 9c-2.3 0-4.5.4-6.5 1.1v3c0 .5-.3.9-.7 1.1l-2.3.9c-.5.2-1-.1-1.2-.6a12 12 0 01.2-8.2C4.7 4.2 8.2 3 12 3s7.3 1.2 10.5 3.3c.6.4.9 1.1.7 1.8a12 12 0 01-.5 1.4c-.2.5-.7.8-1.2.6l-2.3-.9a1.2 1.2 0 01-.7-1.1v-3C16.5 9.4 14.3 9 12 9z"
              transform="rotate(135 12 12)"
            />
          </svg>
        </RoundButton>
      </div>

      {error && (
        <p role="alert" className="text-danger pb-3 text-center text-xs">
          {error}
        </p>
      )}
    </div>
  )
}
