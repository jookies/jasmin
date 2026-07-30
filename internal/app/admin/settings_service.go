package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
)

// Operator-set overrides for gateway settings that are otherwise read once at
// startup from the configuration file.
//
// This deliberately inverts the rule that governs every other object here,
// where the file always wins, so it is narrow on purpose: only settings whose
// consumers can genuinely re-read them at runtime are overridable, and the
// console shows an overridden value as overriding the file rather than
// silently disagreeing with it. Anything an operator would have to restart to
// apply stays file-only, because an editable field that the running process
// ignores is worse than no field at all.
const settingsSchema = `
CREATE TABLE IF NOT EXISTS admin_settings (
    name       TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
);`

// The overridable set. Currency is intentionally absent: it stamps new records
// only, so changing it mid-window splits a customer's usage across two units.
// That is a commercial decision to take in the configuration file, deliberately,
// not a field to nudge in a console.
const (
	SettingCDRRetentionDays              = "cdr_retention_days"
	SettingCDRRetentionBatchSize         = "cdr_retention_batch_size"
	SettingCDRMaintenanceIntervalSeconds = "cdr_maintenance_interval_seconds"
	SettingQuotaPersistIntervalSeconds   = "quota_persist_interval_seconds"
)

var ErrSettingUnknown = errors.New("admin: unknown setting")

// SettingsApplier receives an override the moment it is stored. An
// implementation must apply it to the live runtime or return an error; a store
// write that the process ignores is the failure mode this whole type exists to
// avoid.
type SettingsApplier interface {
	ApplySetting(name string, value int) error
}

type SettingsService struct {
	store   *Store
	applier SettingsApplier
	now     func() string
	mu      sync.Mutex
}

func NewSettingsService(store *Store, applier SettingsApplier, now func() string) (*SettingsService, error) {
	if store == nil || applier == nil {
		return nil, errors.New("admin: settings service needs a store and an applier")
	}
	if now == nil {
		return nil, errors.New("admin: settings service needs a clock")
	}
	if _, err := store.db.Exec(settingsSchema); err != nil {
		return nil, fmt.Errorf("admin: create settings table: %w", err)
	}
	return &SettingsService{store: store, applier: applier, now: now}, nil
}

func validSetting(name string) bool {
	switch name {
	case SettingCDRRetentionDays, SettingCDRRetentionBatchSize,
		SettingCDRMaintenanceIntervalSeconds, SettingQuotaPersistIntervalSeconds:
		return true
	default:
		return false
	}
}

// Overrides returns every stored override, so the caller can apply them at boot
// before the runtime starts consuming the file's values.
func (s *SettingsService) Overrides(ctx context.Context) (map[string]int, error) {
	rows, err := s.store.db.QueryContext(ctx, `SELECT name,value FROM admin_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	overrides := map[string]int{}
	for rows.Next() {
		var name string
		var value int
		if err := rows.Scan(&name, &value); err != nil {
			return nil, err
		}
		if validSetting(name) {
			overrides[name] = value
		}
	}
	return overrides, rows.Err()
}

// Set stores an override and applies it live. The apply happens first: if the
// runtime will not take the value there is no reason to persist it, and a
// stored-but-unapplied setting is exactly the lie this avoids.
func (s *SettingsService) Set(ctx context.Context, name string, value int) error {
	if !validSetting(name) {
		return fmt.Errorf("%w: %q", ErrSettingUnknown, name)
	}
	if value < 0 {
		return fmt.Errorf("%w: %q cannot be negative", ErrInvalidRequest, name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.applier.ApplySetting(name, value); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	_, err := s.store.db.ExecContext(ctx, s.store.rebind(
		`INSERT INTO admin_settings(name,value,updated_at) VALUES(?,?,?)
		 ON CONFLICT(name) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`),
		name, value, s.now())
	return err
}

// Clear removes an override and hands the runtime back the configured value, so
// an operator can always return to what the file says.
func (s *SettingsService) Clear(ctx context.Context, name string, configured int) error {
	if !validSetting(name) {
		return fmt.Errorf("%w: %q", ErrSettingUnknown, name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.applier.ApplySetting(name, configured); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	_, err := s.store.db.ExecContext(ctx,
		s.store.rebind(`DELETE FROM admin_settings WHERE name=?`), name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}
