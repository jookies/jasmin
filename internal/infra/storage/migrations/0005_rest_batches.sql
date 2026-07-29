-- Durable Jasmin REST /secure/sendbatch admission and callback queue.
CREATE TABLE IF NOT EXISTS rest_batches (
    batch_id text PRIMARY KEY,
    accepted_at timestamptz NOT NULL,
    callback_url text NOT NULL DEFAULT '',
    errback_url text NOT NULL DEFAULT '',
    task_count integer NOT NULL CHECK (task_count >= 0)
);

CREATE TABLE IF NOT EXISTS rest_batch_tasks (
    task_id text PRIMARY KEY,
    batch_id text NOT NULL REFERENCES rest_batches(batch_id),
    task_sequence integer NOT NULL CHECK (task_sequence > 0),
    destination text NOT NULL,
    username text NOT NULL,
    credential_digest bytea NOT NULL CHECK (octet_length(credential_digest) = 32),
    request_body bytea NOT NULL,
    state text NOT NULL DEFAULT 'PENDING'
        CHECK (state IN ('PENDING', 'RUNNING', 'DONE')),
    available_at timestamptz NOT NULL,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    lock_owner text,
    locked_until timestamptz,
    successful boolean,
    response_status integer,
    status_text text NOT NULL DEFAULT '',
    last_error text NOT NULL DEFAULT '',
    callback_state text NOT NULL DEFAULT 'NONE'
        CHECK (callback_state IN ('NONE', 'PENDING', 'RUNNING', 'DONE')),
    callback_url text NOT NULL DEFAULT '',
    callback_status integer,
    callback_attempts integer NOT NULL DEFAULT 0 CHECK (callback_attempts >= 0),
    callback_available_at timestamptz,
    callback_lock_owner text,
    callback_locked_until timestamptz,
    completed_at timestamptz,
    callback_completed_at timestamptz,
    UNIQUE (batch_id, task_sequence)
);

CREATE INDEX IF NOT EXISTS rest_batch_tasks_pending
    ON rest_batch_tasks(available_at, batch_id, task_sequence)
    WHERE state = 'PENDING';

CREATE INDEX IF NOT EXISTS rest_batch_callbacks_pending
    ON rest_batch_tasks(callback_available_at, batch_id, task_sequence)
    WHERE callback_state = 'PENDING';
