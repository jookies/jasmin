-- A terminating connector accepts the message itself: it is the destination,
-- not a relay, so it never reaches SMSC_ACCEPTED. Without a state of its own,
-- the receipt it synthesizes was refused by the final-DLR guard forever, and a
-- partner received no delivery receipt while the gateway logged hundreds of
-- rejections a minute.
--
-- Additive: the CHECK gains a value, no row is rewritten, and a rollback only
-- has to tolerate a state it does not recognise.
ALTER TABLE cdr_records DROP CONSTRAINT IF EXISTS cdr_records_state_check;
ALTER TABLE cdr_records ADD CONSTRAINT cdr_records_state_check
    CHECK (state IN (
        'ADMITTED','RETRY_PENDING','UNKNOWN_AFTER_SEND',
        'SMSC_ACCEPTED','SMSC_REJECTED','TERMINAL_TIMEOUT',
        'TERMINATED_LOCALLY'
    ));

ALTER TABLE cdr_events DROP CONSTRAINT IF EXISTS cdr_events_state_check;
ALTER TABLE cdr_events ADD CONSTRAINT cdr_events_state_check
    CHECK (state IN (
        'ADMITTED','RETRY_PENDING','UNKNOWN_AFTER_SEND',
        'SMSC_ACCEPTED','SMSC_REJECTED','TERMINAL_TIMEOUT',
        'TERMINATED_LOCALLY'
    ));
