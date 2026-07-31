-- Plan 021 Step 4 correction: receipts belong to segments, content belongs to
-- the message.
--
-- Under SMPP 3.4 each segment of a concatenated message is its own submit_sm:
-- it gets its own message_id in submit_sm_resp and carries its own
-- registered_delivery flag, and a delivery receipt references a message_id. A
-- partner that sets registered_delivery on every segment is asking for a
-- receipt per segment, and that is what the legacy fake SMSC has always sent
-- them. Producing one receipt for a three-segment message would silently cut
-- their receipt count from N to 1 per long message and leave their
-- reconciliation permanently short.
--
-- So the spool now holds two kinds of row:
--
--   * a deliverable row — one per assembled message, carrying the joined text
--     and scheduled for the downstream push, exactly as before; and
--   * a receipt-only row — one per segment that did not complete its group,
--     carrying the verdict and when its receipt is owed, and nothing else.
--
-- receipt_only tells them apart. It exists as a column rather than being
-- inferred from "text is empty and next_attempt_at is null", because an empty
-- message body is a legitimate message and a pull-only connector legitimately
-- schedules no push: inferring it would make a real message indistinguishable
-- from a fragment on exactly the surface that must never confuse the two.
ALTER TABLE message_spool
    ADD COLUMN IF NOT EXISTS receipt_only boolean NOT NULL DEFAULT false;

-- A receipt-only row must never hold content. The segment's own text is half a
-- message — split mid-rune for any multi-byte encoding — and the assembled row
-- already holds the whole of it, so a second copy would be a second breach
-- surface for OTP bodies that buys nothing. Enforced here as well as in
-- ValidateMessage: this is the copy that survives a future caller.
ALTER TABLE message_spool DROP CONSTRAINT IF EXISTS message_spool_receipt_only_has_no_content;
ALTER TABLE message_spool ADD CONSTRAINT message_spool_receipt_only_has_no_content
    CHECK (NOT receipt_only OR (text = '' AND octet_length(raw) = 0));

-- A receipt-only row must never be scheduled for a downstream push. It has no
-- content to push; scheduling one would walk the row through its whole retry
-- budget and dead-letter it, reporting a delivery failure for a fragment nobody
-- ever intended to deliver.
ALTER TABLE message_spool DROP CONSTRAINT IF EXISTS message_spool_receipt_only_is_not_pushed;
ALTER TABLE message_spool ADD CONSTRAINT message_spool_receipt_only_is_not_pushed
    CHECK (NOT receipt_only OR (next_attempt_at IS NULL AND delivery_state = 'pending'));

-- A receipt-only row exists to owe a receipt. One with no receipt_due_at owes
-- nothing and would sit out its retention holding a segment's metadata for no
-- reason at all.
ALTER TABLE message_spool DROP CONSTRAINT IF EXISTS message_spool_receipt_only_owes_a_receipt;
ALTER TABLE message_spool ADD CONSTRAINT message_spool_receipt_only_owes_a_receipt
    CHECK (NOT receipt_only OR receipt_due_at IS NOT NULL);

-- Cursor paging for a pull consumer, which sees deliverable rows only. The
-- exclusion is part of the index rather than a filter over fetched rows for the
-- same reason the connector scope is: a consumer paging across rows it cannot
-- see gets short pages indistinguishable from "no new messages".
CREATE INDEX IF NOT EXISTS message_spool_deliverable_seq
    ON message_spool(connector_id, seq)
    WHERE NOT receipt_only;
