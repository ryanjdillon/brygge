-- Tag each bank import with the bank account it was downloaded from,
-- so journal entries debit/credit the correct account (1920 drift vs
-- 1925 høyrente vs others) instead of a hard-coded constant.

ALTER TABLE bank_imports
    ADD COLUMN bank_account_code TEXT NOT NULL DEFAULT '1920';
