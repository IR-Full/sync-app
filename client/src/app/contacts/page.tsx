import { ContactsPanel } from '@/features/contacts'

import { RequireSession } from '../require-session'

export default function ContactsPage() {
  return (
    <RequireSession>
      <main className="bg-surface-sunken min-h-dvh">
        <ContactsPanel />
      </main>
    </RequireSession>
  )
}
