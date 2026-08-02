-- Roadmap #18 completion migration. Every operation is idempotent so an
-- existing phase-1 deployment can upgrade in place.
ALTER TABLE cdr_records
    ADD COLUMN IF NOT EXISTS delivery_state text,
    ADD COLUMN IF NOT EXISTS delivery_status text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS delivery_error text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS delivery_done_at timestamptz,
    ADD COLUMN IF NOT EXISTS delivery_received_at timestamptz,
    ADD COLUMN IF NOT EXISTS billing_outcome text NOT NULL DEFAULT 'NOT_APPLICABLE',
    ADD COLUMN IF NOT EXISTS actual_late_amount double precision NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS late_billing_at timestamptz;

UPDATE cdr_records
SET billing_outcome = CASE
    WHEN late_amount > 0 THEN 'PENDING'
    ELSE 'NOT_APPLICABLE'
END
WHERE billing_outcome = 'NOT_APPLICABLE' AND late_amount > 0;

ALTER TABLE cdr_records DROP CONSTRAINT IF EXISTS cdr_records_delivery_state_check;
ALTER TABLE cdr_records ADD CONSTRAINT cdr_records_delivery_state_check
    CHECK (delivery_state IS NULL OR delivery_state IN (
        'DELIVERED','EXPIRED','DELETED','UNDELIVERABLE','REJECTED'
    ));
ALTER TABLE cdr_records DROP CONSTRAINT IF EXISTS cdr_records_billing_outcome_check;
ALTER TABLE cdr_records ADD CONSTRAINT cdr_records_billing_outcome_check
    CHECK (billing_outcome IN ('NOT_APPLICABLE','PENDING','APPLIED','REJECTED'));
ALTER TABLE cdr_records DROP CONSTRAINT IF EXISTS cdr_records_actual_late_amount_check;
ALTER TABLE cdr_records ADD CONSTRAINT cdr_records_actual_late_amount_check
    CHECK (actual_late_amount >= 0);

ALTER TABLE cdr_events
    ADD COLUMN IF NOT EXISTS delivery_state text,
    ADD COLUMN IF NOT EXISTS delivery_status text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS delivery_error text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS delivery_done_at timestamptz,
    ADD COLUMN IF NOT EXISTS billing_outcome text,
    ADD COLUMN IF NOT EXISTS actual_late_amount double precision NOT NULL DEFAULT 0;

-- Every migration in this directory is re-executed in full on each boot, so a
-- constraint definition here is not a one-time step: it is re-asserted forever.
-- That makes a NARROWER definition in an earlier file fatal once a later file
-- widens it — the earlier ADD CONSTRAINT is validated against rows the later
-- one permitted, fails, and the process cannot migrate or take leadership.
-- TERMINATED_LOCALLY therefore belongs here as well as in 0010; every
-- definition of this constraint must list exactly the same values.
ALTER TABLE cdr_events DROP CONSTRAINT IF EXISTS cdr_events_kind_check;
ALTER TABLE cdr_events ADD CONSTRAINT cdr_events_kind_check CHECK (kind IN (
    'ADMITTED','RETRY_PENDING','UNKNOWN_AFTER_SEND',
    'SMSC_ACCEPTED','SMSC_REJECTED','TERMINAL_TIMEOUT',
    'TERMINATED_LOCALLY',
    'FINAL_DLR','LATE_BILLING_APPLIED','LATE_BILLING_REJECTED'
));

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
