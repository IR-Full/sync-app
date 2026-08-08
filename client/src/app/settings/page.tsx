import { SettingsPanel } from '@/widgets/settings-panel'

import { RequireSession } from '../require-session'

export default function SettingsPage() {
  return (
    <RequireSession>
      <main className="bg-surface-sunken min-h-dvh">
        <SettingsPanel />
      </main>
    </RequireSession>
  )
}
