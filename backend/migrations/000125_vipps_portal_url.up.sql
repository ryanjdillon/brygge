-- The address of the club's page in the Vipps MobilePay portal, shown as a link
-- on the payment overview. The number in the portal path is the portal's own
-- identifier, not the merchant serial number, so it cannot be derived from
-- config: the treasurer pastes the address they see after logging in.
-- NULL means no link. The handler accepts only an https address on
-- portal.vippsmobilepay.com.
ALTER TABLE clubs ADD COLUMN IF NOT EXISTS vipps_portal_url TEXT;
