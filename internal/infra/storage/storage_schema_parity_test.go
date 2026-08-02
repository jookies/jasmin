package storage

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The two CDR schemas must allow the same values.
//
// This exists because they silently drifted and a partner stopped receiving
// delivery receipts. Migration 0009 taught the state machine about a message
// this platform terminates itself and widened two of the three constraints
// guarding it — cdr_records_state_check and cdr_events_state_check — but not
// cdr_events_kind_check. PostgreSQL then refused every write of
// Kind = TERMINATED_LOCALLY, which fails the termination connector's
// processing step, which never acks the broker delivery, which redelivers it
// forever. No receipt, and eventually a full submit queue answering
// ESME_RSYSERR.
//
// It shipped because the SQLite schema already listed the value in both
// constraints, so every test running against SQLite passed while the
// PostgreSQL path could not have worked. Comparing the two catches the whole
// class rather than this one instance, and it needs no database.

// checkValues extracts the quoted values of a CHECK (<column> IN (...)) list
// for one column from a chunk of SQL, in any of the three spellings these
// schemas use: inline on the column, as a table constraint, and as an
// ALTER TABLE ... ADD CONSTRAINT.
func checkValues(t *testing.T, sql, column string) []string {
	t.Helper()
	values, ok := findCheckValues(sql, column)
	if !ok {
		t.Fatalf("no CHECK (%s IN (...)) found", column)
	}
	return values
}

// findCheckValues is checkValues without the assertion, so a resolver can ask
// "does this migration define the constraint at all?" using the same matching
// rule rather than a separate string test. The word boundary matters: 0004
// contains `delivery_state IN (...)`, which is a different column, and a
// substring filter would wrongly select that file.
func findCheckValues(sql, column string) ([]string, bool) {
	// Find "<column> ... IN (" and take everything up to the matching ")".
	pattern := regexp.MustCompile(`(?is)\b` + regexp.QuoteMeta(column) + `\b[^()]*?IN\s*\(`)
	location := pattern.FindStringIndex(sql)
	if location == nil {
		return nil, false
	}

	rest := sql[location[1]:]
	end := strings.Index(rest, ")")
	if end < 0 {
		return nil, false
	}

	quoted := regexp.MustCompile(`'([^']+)'`).FindAllStringSubmatch(rest[:end], -1)
	values := make([]string, 0, len(quoted))
	for _, match := range quoted {
		values = append(values, match[1])
	}
	sort.Strings(values)
	return values, true
}

// postgresLatest resolves a constraint the way the database does: migrations
// run in order and each DROP/ADD replaces the previous definition, so the last
// migration that defines the column's CHECK wins.
func postgresLatest(t *testing.T, column string) []string {
	t.Helper()
	var latest []string
	for _, name := range []string{
		"migrations/0003_cdr.sql",
		"migrations/0004_cdr_completion.sql",
		"migrations/0009_cdr_terminated_locally.sql",
		"migrations/0010_cdr_events_kind_terminated_locally.sql",
	} {
		body, err := submitTransactionMigrations.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if values, ok := findCheckValues(string(body), column); ok {
			latest = values
		}
	}
	if latest == nil {
		t.Fatalf("no migration defines the %s constraint", column)
	}
	return latest
}

// postgresEventKindValues resolves the constraint the way the database does:
// later migrations DROP and re-ADD it, so the last definition wins.
func postgresEventKindValues(t *testing.T) []string { return postgresLatest(t, "kind") }

func TestCDREventKindConstraintsAgreeAcrossEngines(t *testing.T) {
	postgres := postgresEventKindValues(t)
	sqlite := checkValues(t, sqliteCDRSchema, "kind")

	if strings.Join(postgres, ",") != strings.Join(sqlite, ",") {
		t.Errorf("cdr_events kind constraint differs between engines:\n  postgres %v\n  sqlite   %v\n"+
			"A value one engine accepts and the other refuses means the SQLite tests pass "+
			"while the PostgreSQL deployment cannot write the row at all.", postgres, sqlite)
	}

	// The specific value whose absence stopped delivery receipts.
	found := false
	for _, value := range postgres {
		if value == "TERMINATED_LOCALLY" {
			found = true
		}
	}
	if !found {
		t.Error("PostgreSQL refuses TERMINATED_LOCALLY event kinds; " +
			"locally-terminated messages cannot record acceptance and their partners get no receipt")
	}
}

func TestCDRStateConstraintsAgreeAcrossEngines(t *testing.T) {
	// The state constraint 0009 did fix — asserted so it cannot regress, and so
	// the pair of tests documents that both columns are guarded.
	postgres := postgresLatest(t, "state")
	sqlite := checkValues(t, sqliteCDRSchema, "state")

	if strings.Join(postgres, ",") != strings.Join(sqlite, ",") {
		t.Errorf("cdr state constraint differs between engines:\n  postgres %v\n  sqlite   %v", postgres, sqlite)
	}
}

// TestMigrationsReassertTheSameConstraint guards the property that makes this
// directory's replay model safe.
//
// Every migration here is executed in full on every boot, so a constraint
// definition is not a one-time step — it is re-asserted forever. A narrower
// definition in an earlier file is therefore fatal the moment a later file
// widens it: on the next boot the earlier ADD CONSTRAINT is validated against
// rows the later one permitted, fails, and the process cannot migrate or take
// leadership. It does not degrade, it does not start.
//
// That is exactly what happened when 0010 widened cdr_events_kind_check while
// 0004 kept re-adding the narrow list: 27 legitimate TERMINATED_LOCALLY rows
// made 0004 unsatisfiable, and the gateway crash-looped in standby.
//
// The invariant is simple enough to test directly: every migration that defines
// a given constraint must list exactly the same values.
func TestMigrationsReassertTheSameConstraint(t *testing.T) {
	for _, column := range []string{"kind", "state"} {
		t.Run(column, func(t *testing.T) {
			definitions := map[string][]string{}
			for _, name := range []string{
				"migrations/0003_cdr.sql",
				"migrations/0004_cdr_completion.sql",
				"migrations/0009_cdr_terminated_locally.sql",
				"migrations/0010_cdr_events_kind_terminated_locally.sql",
			} {
				body, err := submitTransactionMigrations.ReadFile(name)
				if err != nil {
					t.Fatalf("read %s: %v", name, err)
				}
				// Only ALTER TABLE ... ADD CONSTRAINT re-asserts on every boot.
				// A CHECK inside CREATE TABLE IF NOT EXISTS applies once, when
				// the table is first created, and a later ALTER supersedes it —
				// so 0003's original list is not a replay hazard.
				if values, ok := findCheckValues(reassertedSQL(string(body)), column); ok {
					definitions[name] = values
				}
			}
			if len(definitions) < 2 {
				return
			}

			var reference string
			var referenceName string
			for name, values := range definitions {
				joined := strings.Join(values, ",")
				if reference == "" {
					reference, referenceName = joined, name
					continue
				}
				if joined != reference {
					t.Errorf("%s and %s define the %s constraint differently:\n  %s: %s\n  %s: %s\n"+
						"Migrations here replay in full every boot, so the narrower definition "+
						"will be validated against rows the wider one allowed and the gateway "+
						"will fail to start.", referenceName, name, column, referenceName, reference, name, joined)
				}
			}
		})
	}
}

// reassertedSQL keeps only the statements that run against an existing schema on
// every boot: ALTER TABLE ... ADD CONSTRAINT. Everything guarded by
// CREATE TABLE IF NOT EXISTS is create-time-only.
func reassertedSQL(sql string) string {
	var kept []string
	for _, chunk := range strings.Split(sql, "ALTER TABLE") {
		if strings.Contains(chunk, "ADD CONSTRAINT") {
			kept = append(kept, chunk)
		}
	}
	return strings.Join(kept, "\nALTER TABLE ")
}
