-- Plan 021 Step 8: the MT termination connector's message spool.
--
-- One row per assembled message, not per part: the connector reassembles and
-- decodes before it writes here, so a part number would always be 1 of 1.
-- Content is retained for a short window (24 h by default) and pruned in
-- batches; this is a spool, not an archive, because OTP bodies are the
-- highest-value content the platform handles.

-- message_spool_sequence is the cursor allocator, and it is a table rather than
-- a PostgreSQL sequence on purpose.
--
-- A bigserial/identity value is allocated *before* commit, so a writer holding
-- seq=5 can commit after a writer holding seq=6. A consumer paging with
-- "seq > 6" would then skip row 5 forever. Bumping this row inside the writing
-- statement takes a row lock that is held until that statement's transaction
-- commits, which forces sequence order to equal commit order and makes the
-- cursor safe to page with. Gaps are fine; ordering is not.
CREATE TABLE IF NOT EXISTS message_spool_sequence (
    name text PRIMARY KEY,
    last_value bigint NOT NULL CHECK (last_value >= 0)
);
INSERT INTO message_spool_sequence(name, last_value)
    VALUES ('message_spool', 0)
    ON CONFLICT (name) DO NOTHING;

CREATE TABLE IF NOT EXISTS message_spool (
    message_id text PRIMARY KEY,
    -- seq is the paging cursor. It is re-allocated on every mutation, so a row
    -- whose delivery was retried after a consumer paged past it is handed back
    -- rather than silently lost.
    seq bigint NOT NULL,
    connector_id text NOT NULL CHECK (connector_id <> ''),
    user_id text NOT NULL DEFAULT '',
    source_addr text NOT NULL DEFAULT '',
    dest_addr text NOT NULL CHECK (dest_addr <> ''),
    text text NOT NULL DEFAULT '',
    raw bytea NOT NULL DEFAULT ''::bytea,
    data_coding smallint NOT NULL CHECK (data_coding BETWEEN 0 AND 255),
    encoding text NOT NULL DEFAULT '',
    parts integer NOT NULL CHECK (parts > 0),
    verdict_accept boolean NOT NULL,
    verdict_stat text NOT NULL CHECK (verdict_stat <> ''),
    verdict_err text NOT NULL DEFAULT '',
    verdict_reason text NOT NULL DEFAULT '',
    -- gate_bypassed records that the verdict source was unreachable and the
    -- fail-open default was applied, so an infrastructure blip is visible in the
    -- decision trail instead of silently reading as a real decision.
    gate_bypassed boolean NOT NULL DEFAULT false,
    delivery_state text NOT NULL DEFAULT 'pending'
        CHECK (delivery_state IN ('pending', 'delivered', 'dlq')),
    delivery_attempts integer NOT NULL DEFAULT 0 CHECK (delivery_attempts >= 0),
    received_at timestamptz NOT NULL,
    delivered_at timestamptz,
    next_attempt_at timestamptz,
    -- The synthesized receipt is owed to the partner 5-7 s after the submit.
    -- It is persisted rather than left in a goroutine timer so a gateway restart
    -- inside that window still emits it. The lease columns mirror
    -- rest_batch_tasks: a receipt claimed twice is a partner receiving two
    -- receipts for one message, so the claim must be exclusive.
    receipt_due_at timestamptz,
    receipt_sent_at timestamptz,
    receipt_lock_owner text,
    receipt_locked_until timestamptz,
    created_at timestamptz NOT NULL,
    CHECK ((delivery_state = 'delivered') = (delivered_at IS NOT NULL))
);

-- Cursor paging. Unique because two rows sharing a seq would make a cursor
-- ambiguous, which is the failure this column exists to prevent.
CREATE UNIQUE INDEX IF NOT EXISTS message_spool_seq
    ON message_spool(seq);
-- Cursor paging for a consumer scoped to one connector (plan Step 10): the
-- scope has to be part of the index, not a post-filter, or the consumer pages
-- past rows it cannot see and gets short pages it cannot distinguish from
-- "no new messages".
CREATE INDEX IF NOT EXISTS message_spool_connector_seq
    ON message_spool(connector_id, seq);
-- Console/support search: "what did we receive for this number, and when".
CREATE INDEX IF NOT EXISTS message_spool_dest_time
    ON message_spool(dest_addr, received_at, message_id);
-- Time-range search and the retention prune scan.
CREATE INDEX IF NOT EXISTS message_spool_received_at
    ON message_spool(received_at, message_id);
-- Rows due for a downstream delivery attempt.
CREATE INDEX IF NOT EXISTS message_spool_delivery_due
    ON message_spool(next_attempt_at, seq)
    WHERE delivery_state = 'pending' AND next_attempt_at IS NOT NULL;
-- Rows whose receipt is owed and not yet emitted.
CREATE INDEX IF NOT EXISTS message_spool_receipt_due
    ON message_spool(receipt_due_at, seq)
    WHERE receipt_sent_at IS NULL AND receipt_due_at IS NOT NULL;

-- Every read of message text names the actor that performed it. row_count is
-- recorded so a bulk read is distinguishable from a single lookup after the
-- fact.
CREATE TABLE IF NOT EXISTS message_spool_access_audit (
    audit_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    subject text NOT NULL,
    action text NOT NULL,
    target text NOT NULL,
    allowed boolean NOT NULL,
    row_count bigint NOT NULL DEFAULT 0 CHECK (row_count >= 0),
    occurred_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS message_spool_access_audit_time
    ON message_spool_access_audit(occurred_at, audit_id);
