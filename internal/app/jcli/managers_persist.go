package jcli

import (
	"fmt"
	"strings"
)

// persist / load (J-015, J-016, J-017).
//
// Plan 013 Step 7 asked for a decision, and the answer the code makes possible
// is better than the "accepted no-op" the plan proposed:
//
//   - `persist` is truthful as-is. Every admin mutation is applied and written
//     to SQLite in the same operation, so by the time the operator types
//     `persist` the configuration already survives a restart. Reporting the
//     oracle's success lines states a fact, and a script that ends in `persist`
//     keeps working.
//   - `load` is real work, not a no-op: it re-applies the persisted state into
//     the live tables through each service's LoadAndApply, which is exactly
//     what the gateway does at boot. An operator who edits the store out of
//     band, or who wants to discard an uncommitted live change, gets what they
//     asked for.
//
// Named profiles are implemented for real (admin.ProfileService): `persist -p
// NAME` snapshots every admin table under that name and `load -p NAME` restores
// it in one transaction. Accepting the flag and loading something else would
// have been the dangerous option -- an operator rolling back to a known-good
// profile must get that profile.

// defaultProfile is the profile name the oracle uses when none is given.
const defaultProfile = "jcli-prod"

// persistOrder is the order the console reports each manager. It is the order
// the managers are registered in, and scripts that diff transcripts depend on
// it.
var persistOrder = []string{
	"group", "user", "mointerceptor", "mtinterceptor",
	"morouter", "mtrouter", "smppcc", "filter", "httpcc",
}

// profileOf extracts the profile from a `-p NAME` / `--profile=NAME` argument.
func profileOf(argument string) (string, bool) {
	trimmed := strings.TrimSpace(argument)
	if trimmed == "" {
		return defaultProfile, true
	}
	if value, found := strings.CutPrefix(trimmed, "--profile="); found {
		return strings.TrimSpace(value), true
	}
	verb, operand := verbOf(trimmed)
	if verb == "-p" || verb == "--profile" {
		if operand == "" {
			return "", false
		}
		return operand, true
	}
	return "", false
}

func (s *session) handlePersist(argument string) string {
	profile, ok := profileOf(argument)
	if !ok {
		return "Missing required option"
	}
	failed := false
	if s.server.deps.Profiles != nil {
		ctx, cancel := s.context()
		defer cancel()
		failed = s.server.deps.Profiles.Save(ctx, profile) != nil
	}
	lines := make([]string, 0, len(persistOrder))
	for _, manager := range persistOrder {
		if failed {
			lines = append(lines, fmt.Sprintf("Failed to persist %s configuration (profile:%s)", manager, profile))
			continue
		}
		lines = append(lines, fmt.Sprintf("%s configuration persisted (profile:%s)", manager, profile))
	}
	return strings.Join(lines, "\n")
}

func (s *session) handleLoad(argument string) string {
	profile, ok := profileOf(argument)
	if !ok {
		return "Missing required option"
	}
	ctx, cancel := s.context()
	defer cancel()

	// Restore the named snapshot first; the services then re-apply whatever the
	// store now holds. A profile that was never persisted fails every line, as
	// in the oracle.
	if s.server.deps.Profiles != nil {
		if err := s.server.deps.Profiles.Load(ctx, profile); err != nil {
			lines := make([]string, 0, len(persistOrder))
			for _, manager := range persistOrder {
				lines = append(lines, fmt.Sprintf("Failed to load %s configuration (profile:%s)", manager, profile))
			}
			return strings.Join(lines, "\n")
		}
	}

	failures := map[string]bool{}
	if s.server.deps.Groups != nil {
		failures["group"] = s.server.deps.Groups.LoadAndApply(ctx) != nil
	}
	if s.server.deps.Users != nil {
		failures["user"] = s.server.deps.Users.LoadAndApply(ctx) != nil
	}
	if s.server.deps.Interceptors != nil {
		err := s.server.deps.Interceptors.LoadAndApply(ctx)
		failures["mointerceptor"], failures["mtinterceptor"] = err != nil, err != nil
	}
	if s.server.deps.MORoutes != nil {
		failures["morouter"] = s.server.deps.MORoutes.LoadAndApply(ctx) != nil
	}
	if s.server.deps.Routes != nil {
		failures["mtrouter"] = s.server.deps.Routes.LoadAndApply(ctx) != nil
	}
	if s.server.deps.Connectors != nil {
		failures["smppcc"] = s.server.deps.Connectors.LoadAndApply(ctx) != nil
	}
	// Termination connectors are in the snapshot but have no line of their own:
	// persistOrder is a frozen transcript and cannot grow a tenth manager. Their
	// failure is folded into smppcc, the only connector line there is, because a
	// restore that silently left them on the previous config would be worse than
	// a slightly over-broad failure report.
	if s.server.deps.TerminationConnectors != nil {
		if err := s.server.deps.TerminationConnectors.LoadAndApply(ctx); err != nil {
			failures["smppcc"] = true
		}
	}
	// Named filters and HTTP connectors are store-only: nothing to re-apply, so
	// they are loaded by definition.

	lines := make([]string, 0, len(persistOrder))
	for _, manager := range persistOrder {
		if failures[manager] {
			lines = append(lines, fmt.Sprintf("Failed to load %s configuration (profile:%s)", manager, profile))
			continue
		}
		lines = append(lines, fmt.Sprintf("%s configuration loaded (profile:%s)", manager, profile))
	}
	return strings.Join(lines, "\n")
}
