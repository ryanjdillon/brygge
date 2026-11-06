-- A club's current version of a legal document is its latest published row of
-- that type. The public read, the consent record and the join flow all look it
-- up per club, so the index leads with club_id and follows their ORDER BY.
CREATE INDEX IF NOT EXISTS idx_legal_documents_club_type_published
    ON legal_documents (club_id, doc_type, published_at DESC);
