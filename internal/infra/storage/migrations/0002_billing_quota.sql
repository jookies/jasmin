-- Durable billing quotas: the mutable half of every billing principal (the
-- balance and the submit_sm_count ceiling) together with the provisioned
-- baseline it was last granted from. Without this table the gateway keeps
-- balances in process memory only, so a restart re-applies the provisioned
-- balance and silently refunds everything every customer has spent.
--
-- principal_key is the legacy identity — the username, or the gid for a group
-- — never the internal numeric uid. Config-provisioned uids are the account's
-- position in the config array, so keying money on them would move one
-- customer's spent balance onto another the first time that array is reordered.
--
-- A NULL balance / submit_sm_count is legacy "unlimited" (Python None), which
-- is why every quota column is nullable rather than defaulted to zero.
CREATE TABLE IF NOT EXISTS billing_quotas (
    scope text NOT NULL CHECK (scope IN ('user','group')),
    principal_key text NOT NULL,
    balance double precision,
    submit_sm_count bigint,
    provisioned_balance double precision,
    provisioned_submit_sm_count bigint,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (scope, principal_key)
);
