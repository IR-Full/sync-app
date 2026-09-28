-- Messages deleted before DeleteMessage learned to clear the attachment still
-- carry it: file name, size, media and thumbnail refs. Clear them so history
-- stops returning them and the media collector can reclaim the blobs.
UPDATE messages SET attachment = NULL WHERE deleted AND attachment IS NOT NULL;
