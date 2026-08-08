'use client'

import { useQuery } from '@tanstack/react-query'
import { useCallback, useState } from 'react'

import type { MessageAttachment } from '@/entities/message'
import { MsgType, useIsConnected, useSynapseClient, type Wire } from '@/shared/api'

/**
 * Rewrites a gateway-issued absolute URL onto this app's origin.
 *
 * The server builds upload/download URLs from SYNAPSE_PUBLIC_URL, so they point
 * straight at the gateway (e.g. http://localhost:8080/media/...). Since the
 * gateway sends no CORS headers, the browser must talk to its own origin
 * instead; `next.config.ts` proxies /media/* through to the gateway. Keeping
 * only the path+query does exactly that, and is harmless when the app is already
 * served from the same host.
 */
export function toSameOriginMediaUrl(absoluteUrl: string): string {
  try {
    const parsed = new URL(absoluteUrl)
    return `${parsed.pathname}${parsed.search}`
  } catch {
    return absoluteUrl
  }
}

export interface UploadResult {
  mediaRef: string
  attachment: MessageAttachment
}

/** Media kind inferred from the MIME type — drives how the bubble renders it. */
function kindOf(file: File): string {
  if (file.type.startsWith('image/')) return 'image'
  if (file.type.startsWith('video/')) return 'video'
  if (file.type.startsWith('audio/')) return 'voice'
  return 'file'
}

/** Reads an image's intrinsic size so the bubble can reserve space before load. */
async function imageDimensions(file: File): Promise<{ width: number; height: number }> {
  if (!file.type.startsWith('image/')) return { width: 0, height: 0 }
  const url = URL.createObjectURL(file)
  try {
    const size = await new Promise<{ width: number; height: number }>((resolve) => {
      const image = new Image()
      image.onload = () => resolve({ width: image.naturalWidth, height: image.naturalHeight })
      image.onerror = () => resolve({ width: 0, height: 0 })
      image.src = url
    })
    return size
  } finally {
    URL.revokeObjectURL(url)
  }
}

/**
 * Uploads a file and returns the reference to attach to a message.
 *
 * Three steps, mirroring the server's pipeline: MEDIA_INIT mints a signed,
 * size-bound ticket; the bytes go over plain HTTP to that ticket's URL; only
 * then does a message carrying the ref get sent. Media never travels as a
 * protocol frame — the 16 MiB frame cap exists precisely to keep it out.
 *
 * The declared size is signed into the ticket and the upload handler rejects a
 * body that is even one byte off, so the file must be sent exactly as measured.
 */
export function useMediaUpload() {
  const client = useSynapseClient()
  const [progress, setProgress] = useState<number | null>(null)

  const upload = useCallback(
    async (file: File): Promise<UploadResult> => {
      setProgress(0)
      try {
        const ticket = await client.request<Wire.MediaTicket>(
          MsgType.MEDIA_INIT,
          {
            filename: file.name,
            contentType: file.type || 'application/octet-stream',
            size: file.size,
          },
          { expect: MsgType.MEDIA_TICKET },
        )

        const response = await fetch(toSameOriginMediaUrl(ticket.body.uploadUrl), {
          method: 'PUT',
          headers: { 'Content-Type': file.type || 'application/octet-stream' },
          body: file,
        })
        if (!response.ok) {
          throw new Error(`upload failed: ${response.status} ${await response.text()}`)
        }
        setProgress(100)

        const { width, height } = await imageDimensions(file)
        return {
          mediaRef: ticket.body.mediaRef,
          attachment: {
            kind: kindOf(file),
            mediaRef: ticket.body.mediaRef,
            filename: file.name,
            mime: file.type || 'application/octet-stream',
            size: file.size,
            durationMs: 0,
            waveform: [],
            width,
            height,
            thumbRef: '',
          },
        }
      } finally {
        setProgress(null)
      }
    },
    [client],
  )

  return { upload, progress }
}

/**
 * Resolves a media ref to a signed, expiring download URL.
 *
 * Cached for less than the server's own TTL so a link is refreshed before it
 * expires rather than after it has already 403'd.
 */
export function useMediaUrl(mediaRef: string) {
  const client = useSynapseClient()
  const connected = useIsConnected()

  return useQuery({
    queryKey: ['media', mediaRef],
    enabled: connected && mediaRef.length > 0,
    staleTime: 4 * 60_000,
    gcTime: 5 * 60_000,
    retry: 1,
    queryFn: async (): Promise<string> => {
      const reply = await client.request<Wire.MediaURL>(
        MsgType.MEDIA_FETCH,
        { mediaRef },
        { expect: MsgType.MEDIA_URL },
      )
      return toSameOriginMediaUrl(reply.body.downloadUrl)
    },
  })
}
