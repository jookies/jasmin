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
        'SMSC_ACCEPTED','SMSC_REJECTED','TERMINAL_TIMEOUT'
    )),
    state text NOT NULL CHECK (state IN (
        'ADMITTED','RETRY_PENDING','UNKNOWN_AFTER_SEND',
        'SMSC_ACCEPTED','SMSC_REJECTED','TERMINAL_TIMEOUT'
    )),
    attempt_id bigint,
    smpp_status text NOT NULL DEFAULT '',
    smsc_message_id text,
    occurred_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS cdr_events_record_time ON cdr_events(cdr_id, occurred_at, event_key);
