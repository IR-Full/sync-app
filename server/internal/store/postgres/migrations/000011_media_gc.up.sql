-- Media garbage collection.
--
-- The collector asks one question per blob — "does any LIVE message still point
-- at this?" — and asks it for every object in the store on each sweep. Without
-- these indexes that is a sequential scan of the message log per object, which
-- turns a housekeeping job into an outage.
--
-- Both are partial: only rows that actually carry a ref and are not tombstoned
-- can keep a blob alive, so the index covers exactly the rows the query looks at
-- and stays a small fraction of the table.
CREATE INDEX IF NOT EXISTS messages_media_ref_idx
    ON messages (media_ref) WHERE media_ref <> '' AND deleted = FALSE;

-- Typed attachments carry their own ref (and an optional thumbnail), so a
-- message can hold a blob without media_ref being set at all.
CREATE INDEX IF NOT EXISTS messages_attachment_ref_idx
    ON messages ((attachment->>'media_ref')) WHERE attachment IS NOT NULL AND deleted = FALSE;
CREATE INDEX IF NOT EXISTS messages_attachment_thumb_idx
    ON messages ((attachment->>'thumb_ref')) WHERE attachment IS NOT NULL AND deleted = FALSE;
