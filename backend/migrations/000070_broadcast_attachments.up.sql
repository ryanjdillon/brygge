-- Persist attachment/inline-image body parts on a bulk send so the
-- delivery worker can re-attach them to each fanned-out message (BRY-186).
-- JSONB array of JMAP attach body parts (blobId/type/name/disposition/cid).

ALTER TABLE broadcasts ADD COLUMN IF NOT EXISTS attachments JSONB;
