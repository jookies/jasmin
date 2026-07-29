-- Roadmap #18: content-free, append-only CDR lifecycle plus current projection.
CREATE TABLE IF NOT EXISTS cdr_records (
    cdr_id text PRIMARY KEY,
    message_id text NOT NULL,
    part_number integer NOT NULL CHECK (part_number > 0),
    part_count integer NOT NULL CHECK (part_count >= part_number),
    user_id text NOT NULL DEFAULT '',
    group_id text NOT NULL DEFAULT '',
    route_id text NOT NULL,
    connector_id text NOT NULL,
    ingress text NOT NULL DEFAULT '',
    bill_id text NOT NULL DEFAULT '',
    rate double precision NOT NULL CHECK (rate >= 0),
    currency text NOT NULL CHECK (char_length(currency) = 3),
    early_amount double precision NOT NULL CHECK (early_amount >= 0),
    late_amount double precision NOT NULL CHECK (late_amount >= 0),
    billing_mode text NOT NULL CHECK (billing_mode IN ('FREE','PREPAID','POSTPAID','SPLIT')),
    state text NOT NULL CHECK (state IN (
        'ADMITTED','RETRY_PENDING','UNKNOWN_AFTER_SEND',
        'SMSC_ACCEPTED','SMSC_REJECTED','TERMINAL_TIMEOUT'
    )),
    attempt_id bigint,
    smpp_status text NOT NULL DEFAULT '',
    smsc_message_id text,
    delivery_state text CHECK (delivery_state IS NULL OR delivery_state IN (
        'DELIVERED','EXPIRED','DELETED','UNDELIVERABLE','REJECTED'
    )),
    delivery_status text NOT NULL DEFAULT '',
    delivery_error text NOT NULL DEFAULT '',
    delivery_done_at timestamptz,
    delivery_received_at timestamptz,
    billing_outcome text NOT NULL DEFAULT 'NOT_APPLICABLE' CHECK (billing_outcome IN (
        'NOT_APPLICABLE','PENDING','APPLIED','REJECTED'
    )),
    actual_late_amount double precision NOT NULL DEFAULT 0 CHECK (actual_late_amount >= 0),
    late_billing_at timestamptz,
    admitted_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    terminal_at timestamptz
);
CREATE INDEX IF NOT EXISTS cdr_records_message_id ON cdr_records(message_id, part_number);
CREATE INDEX IF NOT EXISTS cdr_records_user_time ON cdr_records(user_id, admitted_at, cdr_id);
CREATE INDEX IF NOT EXISTS cdr_records_terminal_time ON cdr_records(terminal_at, cdr_id)
    WHERE terminal_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS cdr_events (
    event_key text PRIMARY KEY,
    cdr_id text NOT NULL REFERENCES cdr_records(cdr_id),
    kind text NOT NULL CHECK (kind IN (
        'ADMITTED','RETRY_PENDING','UNKNOWN_AFTER_SEND',
        'SMSC_ACCEPTED','SMSC_REJECTED','TERMINAL_TIMEOUT',
        'FINAL_DLR','LATE_BILLING_APPLIED','LATE_BILLING_REJECTED'
    )),
    state text NOT NULL CHECK (state IN (
        'ADMITTED','RETRY_PENDING','UNKNOWN_AFTER_SEND',
        'SMSC_ACCEPTED','SMSC_REJECTED','TERMINAL_TIMEOUT'
    )),
    attempt_id bigint,
    smpp_status text NOT NULL DEFAULT '',
    smsc_message_id text,
    delivery_state text,
    delivery_status text NOT NULL DEFAULT '',
    delivery_error text NOT NULL DEFAULT '',
    delivery_done_at timestamptz,
    billing_outcome text,
    actual_late_amount double precision NOT NULL DEFAULT 0,
    occurred_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS cdr_events_record_time ON cdr_events(cdr_id, occurred_at, event_key);

CREATE TABLE IF NOT EXISTS cdr_access_audit (
    audit_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    subject text NOT NULL,
    action text NOT NULL,
    target text NOT NULL,
    allowed boolean NOT NULL,
    occurred_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS cdr_access_audit_time
    ON cdr_access_audit(occurred_at, audit_id);
