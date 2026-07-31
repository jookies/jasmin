-- Plan 021 Step 10: scoped, read-only credentials for the message pull API.
--
-- Numbered 0008 rather than 0007 to leave that number free for the concurrent
-- per-segment receipt work on message_spool. These files carry no version table
-- and are applied by name, so a gap costs nothing and a collision would cost a
-- merge conflict in a migration that has already run somewhere.
--
-- The pull API exists so a downstream application can fetch decoded messages by
-- cursor instead of (or as well as) having them pushed. It authenticates with
-- one of these rows and never with the admin token: the admin token creates
-- users, moves balances and starts connectors, and handing it to a partner's
-- application to read messages would make every one of those things reachable
-- from that application's process.

CREATE TABLE IF NOT EXISTS message_spool_consumers (
    consumer_id text PRIMARY KEY,
    label text NOT NULL DEFAULT '',
    -- token_sha256 is the proof, never the credential: SHA-256 of the presented
    -- token, exactly as rest_batch_tasks.credential_digest stores a submit
    -- credential. Unsalted and uniterated is correct here and would not be for a
    -- password — the input is 32 bytes from crypto/rand, so there is no
    -- dictionary to precompute, and a salt would turn a per-poll unique index
    -- hit into a table scan.
    --
    -- UNIQUE is load-bearing beyond de-duplication: it is what makes
    -- authentication a single indexed lookup instead of a scan whose duration
    -- would vary with how many credentials exist.
    token_sha256 bytea NOT NULL UNIQUE CHECK (octet_length(token_sha256) = 32),
    -- scope_json is the whole scope object, not a column per rule.
    --
    -- The anticipated rule types — destination prefix, source address, partner,
    -- maximum lookback — then arrive as fields inside this document rather than
    -- as a migration each, and an older gateway reading a newer row ignores what
    -- it does not know instead of failing to start. Nothing queries a scope in
    -- SQL: it is read into the process and compiled into the spool query's
    -- predicate, which is where it has to be enforced anyway.
    scope_json text NOT NULL,
    -- revoked turns a credential off without deleting it, so the access-audit
    -- rows it already produced keep naming an id that can still be looked up.
    revoked boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    -- last_used_at is written at most once a minute per consumer. A credential
    -- polling every second would otherwise generate 86 400 writes a day to
    -- record something nobody reads at that resolution. NULL is the state worth
    -- looking for: a token that was issued and never used.
    last_used_at timestamptz
);

CREATE INDEX IF NOT EXISTS message_spool_consumers_last_used
    ON message_spool_consumers(last_used_at);
