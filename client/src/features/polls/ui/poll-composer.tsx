'use client'

import { useState } from 'react'

import { useTranslate } from '@/shared/i18n'
import { Button, ErrorNote, Modal, TextField, Toggle } from '@/shared/ui'

import { usePollActions } from '../model/use-polls'

const MAX_OPTIONS = 10

export function PollComposer({
  chatId,
  open,
  onClose,
}: {
  chatId: string
  open: boolean
  onClose: () => void
}) {
  const t = useTranslate()
  const { create } = usePollActions(chatId)

  const [question, setQuestion] = useState('')
  const [options, setOptions] = useState(['', ''])
  const [multiChoice, setMultiChoice] = useState(false)
  const [anonymous, setAnonymous] = useState(false)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const filled = options.filter((option) => option.trim()).length
  const canSubmit = question.trim().length > 0 && filled >= 2

  async function submit() {
    setPending(true)
    setError(null)
    try {
      await create(question, options, multiChoice, anonymous)
      setQuestion('')
      setOptions(['', ''])
      onClose()
    } catch {
      setError(t('error.unknown'))
    } finally {
      setPending(false)
    }
  }

  return (
    <Modal open={open} onClose={onClose} title={t('poll.create')}>
      <div className="flex flex-col gap-3">
        <TextField
          label={t('poll.question')}
          value={question}
          onChange={(event) => setQuestion(event.target.value)}
        />

        {options.map((option, index) => (
          <TextField
            key={index}
            label={t('poll.option', { index: index + 1 })}
            value={option}
            onChange={(event) =>
              setOptions((current) =>
                current.map((value, position) =>
                  position === index ? event.target.value : value,
                ),
              )
            }
          />
        ))}

        {options.length < MAX_OPTIONS && (
          <Button
            variant="secondary"
            size="small"
            onClick={() => setOptions((current) => [...current, ''])}
          >
            {t('poll.addOption')}
          </Button>
        )}

        <Toggle label={t('poll.multi')} checked={multiChoice} onChange={setMultiChoice} />
        <Toggle label={t('poll.anonymous')} checked={anonymous} onChange={setAnonymous} />

        {error && <ErrorNote>{error}</ErrorNote>}

        <div className="flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button onClick={submit} loading={pending} disabled={!canSubmit}>
            {t('poll.submit')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}
