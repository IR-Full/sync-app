-- User avatars.
--
-- An avatar is a media_ref, not bytes: the image goes through the same upload,
-- signing and garbage-collection pipeline as any attachment, so nothing here
-- has to know how images are stored. NOT NULL DEFAULT '' keeps "no avatar" a
-- value rather than a NULL every read has to remember to handle.
ALTER TABLE users ADD COLUMN IF NOT EXISTS avatar_ref TEXT NOT NULL DEFAULT '';
