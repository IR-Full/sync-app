'use client'

import { useTranslate } from '@/shared/i18n'
import { cn } from '@/shared/lib/cn'
import { Button, ErrorNote, Modal, Spinner } from '@/shared/ui'

import { useSafetyNumbers } from '../model/use-safety'

/**
 * The out-of-band verification screen.
 *
 * Two jobs, and only the second one is optional. It shows the safety number so
 * two people can compare it over a channel the server does not control — that is
 * the only thing that catches a directory serving each side a different identity
 * key. And it is where a CHANGED key gets resolved: pinning refuses to send to a
 * device whose identity moved, so this panel is the way out of that, by a human
 * deciding rather than the app guessing.
 *
 * The wording avoids "verified". Comparing digits proves the keys match right
 * now; it does not make anything trustworthy in general, and a green checkmark
 * saying otherwise would be the most misleading thing on the screen.
 */
export function SafetyPanel({
  peerUserId,
  peerLabel,
  open,
  onClose,
}: {
  peerUserId: string
  peerLabel: string
  open: boolean
  onClose: () => void
}) {
  const t = useTranslate()
  // Gated on `open` so closing the panel stops the fetch and opening it starts a
  // fresh one — the number has to reflect the keys in use right now, not the
  // ones that were in use when the chat was opened.
  const { query, accept } = useSafetyNumbers(peerUserId, open)
  const devices = query.data

  return (
    <Modal open={open} onClose={onClose} title={t('safety.title', { name: peerLabel })}>
      <p className="text-ink-faint mb-3 text-xs">{t('safety.explainer')}</p>

      {query.isFetching && (
        <p className="text-ink-faint flex items-center gap-2 text-sm">
          <Spinner className="size-4" /> {t('safety.loading')}
        </p>
      )}
      {query.isError && <ErrorNote className="mb-2">{t('safety.failed')}</ErrorNote>}

      {devices?.length === 0 && (
        <p className="text-ink-faint text-sm">{t('secret.noDevices')}</p>
      )}

      <ul className="flex flex-col gap-3">
        {devices?.map((device) => (
          <li key={device.deviceId} className="border-line rounded-xl border p-3">
            <p
              className={cn(
                'mb-1 text-xs font-medium',
                device.status === 'changed' ? 'text-danger' : 'text-ink-faint',
              )}
            >
              {device.status === 'changed'
                ? t('safety.changed')
                : device.status === 'pinned'
                  ? t('safety.pinned')
                  : t('safety.unpinned')}
            </p>

            {/* Monospace and grouped: the number exists to be read aloud or
                compared character by character, and a proportional font makes
                both harder than they need to be. */}
            <p className="text-ink font-mono text-sm leading-6 tracking-wide break-all">
              {device.number}
            </p>

            {device.status !== 'pinned' && (
              <Button
                className="mt-2"
                size="small"
                variant={device.status === 'changed' ? 'danger' : 'secondary'}
                onClick={() => accept.mutate(device)}
                disabled={accept.isPending}
              >
                {device.status === 'changed' ? t('safety.acceptChange') : t('safety.pin')}
              </Button>
            )}
            {device.status === 'changed' && (
              <p className="text-ink-faint mt-1 text-xs">{t('safety.changedHint')}</p>
            )}
          </li>
        ))}
      </ul>

      <div className="mt-4 flex gap-2">
        <Button
          variant="secondary"
          onClick={() => void query.refetch()}
          disabled={query.isFetching}
        >
          {t('safety.refresh')}
        </Button>
        <Button variant="ghost" onClick={onClose}>
          {t('common.close')}
        </Button>
      </div>
    </Modal>
  )
}
