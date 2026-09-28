-- Whether message TEXT may be sent to the push provider.
--
-- The notification path put a 120-rune preview of every message into the payload
-- it handed to FCM/APNs, so Apple and Google saw the contents of every
-- conversation on the system. That is a larger disclosure than anything
-- end-to-end encryption was protecting against: E2E guards against the server,
-- and this was the server volunteering the text to a third party.
--
-- DEFAULT FALSE, which is the one place these settings deliberately do not
-- preserve the previous behaviour. privacy_last_seen and its siblings default to
-- 'everyone' because tightening an existing account's visibility reads, from the
-- inside, as the app breaking. This one defaults closed because the previous
-- behaviour was a leak rather than a preference — nobody chose it, so nobody is
-- having a choice taken away.
ALTER TABLE users
  ADD COLUMN IF NOT EXISTS privacy_push_preview BOOLEAN NOT NULL DEFAULT FALSE;
