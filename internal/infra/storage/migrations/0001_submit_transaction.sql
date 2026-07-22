-- Wave 1A Task 1.3: durable submit transaction, response and outbox lane.
CREATE TABLE IF NOT EXISTS submit_parts (
    part_key text PRIMARY KEY,
    message_id text NOT NULL,
    part_number integer NOT NULL CHECK (part_number > 0),
    connector_id text NOT NULL,
    user_id text NOT NULL DEFAULT '',
    bill_id text NOT NULL DEFAULT '',
    state text NOT NULL CHECK (state IN ('PENDING','ATTEMPTING','UNKNOWN_AFTER_SEND','RESULT_COMMITTED')),
    created_at timestamptz NOT NULL,
    UNIQUE (message_id, part_number)
);

CREATE TABLE IF NOT EXISTS submit_attempts (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    part_key text NOT NULL REFERENCES submit_parts(part_key),
    attempt_number integer NOT NULL CHECK (attempt_number > 0),
    state text NOT NULL CHECK (state IN ('INTENT','SENT','UNKNOWN_AFTER_SEND','RESULT_COMMITTED')),
    created_at timestamptz NOT NULL,
    sent_at timestamptz,
    resolved_at timestamptz,
    UNIQUE (part_key, attempt_number)
);

CREATE TABLE IF NOT EXISTS submit_results (
    attempt_id bigint PRIMARY KEY REFERENCES submit_attempts(id),
    part_key text NOT NULL REFERENCES submit_parts(part_key),
    kind text NOT NULL CHECK (kind IN ('SUCCESS','RETRY','FAILURE','TIMEOUT')),
    smpp_status text NOT NULL DEFAULT '',
    smsc_message_id text,
    committed_at timestamptz NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS submit_results_final_part
    ON submit_results(part_key)
    WHERE kind <> 'RETRY';
-- SMSC message IDs are opaque and may repeat across connectors, accounts, or
-- SMSC restarts. Result idempotency is owned by attempt_id/final part keys;
-- this is a lookup index, never a global uniqueness constraint.
DROP INDEX IF EXISTS submit_results_smsc_message_id;
CREATE INDEX IF NOT EXISTS submit_results_smsc_message_id_lookup
    ON submit_results(smsc_message_id)
    WHERE smsc_message_id IS NOT NULL AND smsc_message_id <> '';

CREATE TABLE IF NOT EXISTS submit_outbox (
    event_key text PRIMARY KEY,
    part_key text NOT NULL REFERENCES submit_parts(part_key),
    kind text NOT NULL CHECK (kind IN ('SUBMIT_REQUEST','SUBMIT_RESPONSE','LATE_BILLING','DLR_STATE')),
    exchange_name text NOT NULL,
    routing_key text NOT NULL,
    payload bytea NOT NULL,
    created_at timestamptz NOT NULL,
    available_at timestamptz NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    dispatched_at timestamptz,
    lock_owner text,
    locked_until timestamptz,
    last_error text
);
CREATE INDEX IF NOT EXISTS submit_outbox_pending
    ON submit_outbox(available_at, created_at, event_key)
    WHERE dispatched_at IS NULL;
CREATE INDEX IF NOT EXISTS submit_outbox_part_order_pending
    ON submit_outbox(part_key, created_at, event_key)
    WHERE dispatched_at IS NULL;

CREATE TABLE IF NOT EXISTS submit_billing_intents (
    event_key text PRIMARY KEY REFERENCES submit_outbox(event_key),
    part_key text NOT NULL REFERENCES submit_parts(part_key),
    applied_at timestamptz
);
