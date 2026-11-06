-- Settling a join payment grants membership (BRY-630). The welcome email, the
-- board's "new member" notice, and the board's notices of a refunded join
-- payment and of a grant withheld by the integrity check are written to the shared delivery queue in the transaction that
-- grants or records them, and the delivery worker sends them after the
-- commit. They hang off no broadcast or faktura, so the parent-by-kind check
-- gains a membership_* branch with neither parent set. user_id is not required
-- by the check: it is ON DELETE SET NULL, and an erased member must not make
-- the delete fail; the sender treats a row without a user as done.
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'deliveries_parent_by_kind' AND conrelid = 'deliveries'::regclass) THEN
    ALTER TABLE deliveries DROP CONSTRAINT deliveries_parent_by_kind;
  END IF;
  ALTER TABLE deliveries ADD CONSTRAINT deliveries_parent_by_kind CHECK (
      (kind = 'broadcast' AND broadcast_id IS NOT NULL AND invoice_id IS NULL)
      OR (kind LIKE 'invoice_%' AND invoice_id IS NOT NULL AND broadcast_id IS NULL)
      OR (kind LIKE 'membership_%' AND broadcast_id IS NULL AND invoice_id IS NULL)
  );
END $$;

-- One refund notice and one withheld-grant notice per join source (the join
-- order, or the join faktura): the key is payload->>'ref'. A refund or a
-- withheld grant seen again (a webhook redelivery, the hourly sweep, a credit
-- note on a faktura a card refund already refunded) finds the row and queues
-- nothing.
CREATE UNIQUE INDEX IF NOT EXISTS idx_deliveries_membership_notice_ref
    ON deliveries (kind, (payload->>'ref'))
    WHERE kind IN ('membership_join_refunded', 'membership_grant_withheld');

-- Where the board's membership notices go. Empty means the deployment default:
-- the club's general shared mailbox (post@) from the board-mailbox spec, else
-- the contact-form recipient (secretary, else chair).
ALTER TABLE clubs ADD COLUMN IF NOT EXISTS board_notification_email TEXT NOT NULL DEFAULT '';
