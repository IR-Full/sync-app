import { ProfilePanel } from '@/widgets/profile-panel'

import { RequireSession } from '../require-session'

export default function ProfilePage() {
  return (
    <RequireSession>
      <main className="bg-surface-sunken min-h-dvh">
        <ProfilePanel />
      </main>
    </RequireSession>
  )
}
