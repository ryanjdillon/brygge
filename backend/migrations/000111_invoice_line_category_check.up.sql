-- Every invoice line with a price item stores that item's category at issue
-- (BRY-389). GL booking resolves a line's revenue account from the stored
-- category, so a line without one would book by the item's current category
-- and move if the item were re-categorised later. Lines with no price item
-- (custom and imported lines) are unaffected.
--
-- The existence check is scoped to this table rather than the constraint
-- name alone, so the guard still adds the constraint when the migrations run
-- in several schemas of one database (the integration-test setup).
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'invoice_lines_price_item_category'
           AND conrelid = 'invoice_lines'::regclass
    ) THEN
        ALTER TABLE invoice_lines ADD CONSTRAINT invoice_lines_price_item_category
            CHECK (price_item_id IS NULL OR category IS NOT NULL);
    END IF;
END
$$;
