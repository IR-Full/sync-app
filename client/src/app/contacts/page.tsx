import { ContactsPanel } from '@/features/contacts'

import { RequireSession } from '../require-session'

export default function ContactsPage() {
  return (
    <RequireSession>
      <main className="min-h-dvh bg-surface-sunken">
        <ContactsPanel />
      </main>
    </RequireSession>
  )
}
