-- Finish what 0009 started.
--
-- 0009 taught the CDR state machine about a message this platform terminates
-- itself, and it fixed two of the three constraints that guard it:
-- cdr_records_state_check and cdr_events_state_check. It missed
-- cdr_events_kind_check, which still carried the list from 0004.
--
-- postgres_cdr.go writes the terminal event with Kind = TERMINATED_LOCALLY and
-- State = TERMINATED_LOCALLY. With only the state constraint widened, every
-- such write was refused:
--
--   ERROR: new row for relation "cdr_events" violates check constraint
--          "cdr_events_kind_check" (SQLSTATE 23514)
--
-- The consequence is not a bookkeeping annoyance. The termination connector
-- spools the message durably, then records acceptance on the CDR; that record
-- is what makes the synthesized receipt admissible later. The failing insert
-- fails the whole processing step, so the broker delivery is never acked, the
-- message is redelivered, and it fails again — forever. A partner therefore
-- receives no delivery receipt at all, the submit queue fills with the same
-- poison message, and once it is full new submits are answered ESME_RSYSERR.
--
-- It reached production because the SQLite schema (sqlite_cdr.go) lists
-- TERMINATED_LOCALLY in both its kind and state CHECKs, so every test that ran
-- against SQLite passed while PostgreSQL refused the same write.
-- storage_schema_parity_test.go now compares the two and fails when they drift.
--
-- Additive: the CHECK gains a value, no row is rewritten, and a rollback only
-- has to tolerate a kind it does not recognise.
ALTER TABLE cdr_events DROP CONSTRAINT IF EXISTS cdr_events_kind_check;
ALTER TABLE cdr_events ADD CONSTRAINT cdr_events_kind_check CHECK (kind IN (
    'ADMITTED','RETRY_PENDING','UNKNOWN_AFTER_SEND',
    'SMSC_ACCEPTED','SMSC_REJECTED','TERMINAL_TIMEOUT',
    'TERMINATED_LOCALLY',
    'FINAL_DLR','LATE_BILLING_APPLIED','LATE_BILLING_REJECTED'
));
