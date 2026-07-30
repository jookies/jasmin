package jcli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pumpitspace/synevyr/internal/app/outbound"
)

// Group management (J-003). Every literal here is from
// spec/compatibility/fixtures/jcli/J-003-group.jsonl.

// groupKind is the interactive contract for `group -a`.
var groupKind = &entityKind{
	keyLabel:        "Group",
	addAnnouncement: "Adding a new Group: (ok: save, ko: exit)",
	updateAnnouncement: func(gid string) string {
		return fmt.Sprintf("Updating Group id [%s]: (ok: save, ko: exit)", gid)
	},
	required: []string{"gid"},
	validate: func(is *interactiveSession, key, value string) (string, bool) {
		if key != "gid" {
			return fmt.Sprintf("Unknown Group key: %s", key), false
		}
		if !legacyGroupID.MatchString(value) {
			return "Error: Group gid syntax is invalid", false
		}
		is.set(key, value)
		return "", true
	},
	save: func(s *session, is *interactiveSession) (string, bool) {
		gid := is.values["gid"]
		spec, err := json.Marshal(outbound.GroupConfig{GID: gid})
		if err != nil {
			return fmt.Sprintf("Error: %v", err), false
		}
		ctx, cancel := s.context()
		defer cancel()
		if err := s.server.deps.Groups.CreateGroup(ctx, gid, string(spec)); err != nil {
			return fmt.Sprintf("Error: %v", err), false
		}
		return fmt.Sprintf("Successfully added Group [%s]", gid), true
	},
}

func (s *session) handleGroup(argument string) string {
	verb, operand := verbOf(argument)
	switch {
	case isListVerb(verb):
		return s.listGroups()
	case verb == "-a" || verb == "--add":
		return s.startInteractive(groupKind, false, "")
	case verb == "-r" || verb == "--remove":
		return s.removeGroup(operand)
	case verb == "-e" || verb == "--enable":
		return s.setGroupEnabled(operand, true)
	case verb == "-d" || verb == "--disable":
		return s.setGroupEnabled(operand, false)
	case verb == "":
		return "Missing required option"
	default:
		return unsupportedVerb("group", verb)
	}
}

func (s *session) listGroups() string {
	ctx, cancel := s.context()
	defer cancel()
	stored, err := s.server.deps.Groups.ListGroups(ctx)
	if err != nil {
		return err.Error()
	}
	var lines []string
	if len(stored) > 0 {
		lines = append(lines, "#"+padRight("Group id", 16))
		for _, entry := range stored {
			var group outbound.GroupConfig
			if err := json.Unmarshal([]byte(entry.SpecJSON), &group); err != nil {
				return fmt.Sprintf("group %q: stored spec is not valid JSON: %v", entry.GID, err)
			}
			// A disabled entity is listed with a "!" prefix on its id.
			label := entry.GID
			if group.Disabled {
				label = "!" + label
			}
			lines = append(lines, "#"+padRight(label, 16))
		}
	}
	lines = append(lines, fmt.Sprintf("Total Groups: %d", len(stored)))
	return strings.Join(lines, "\n")
}

func (s *session) removeGroup(gid string) string {
	if gid == "" {
		return "Missing required option"
	}
	ctx, cancel := s.context()
	defer cancel()
	if err := s.server.deps.Groups.DeleteGroup(ctx, gid); err != nil {
		return fmt.Sprintf("Unknown Group: %s", gid)
	}
	return fmt.Sprintf("Successfully removed Group id:%s", gid)
}

// setGroupEnabled toggles the group's enabled flag, which the list renders as a
// "!" prefix. The stored spec is rewritten rather than patched in place so the
// live directory and the store never disagree about the flag.
func (s *session) setGroupEnabled(gid string, enabled bool) string {
	if gid == "" {
		return "Missing required option"
	}
	ctx, cancel := s.context()
	defer cancel()
	stored, err := s.server.deps.Groups.GetGroup(ctx, gid)
	if err != nil {
		return fmt.Sprintf("Unknown Group: %s", gid)
	}
	var group outbound.GroupConfig
	if err := json.Unmarshal([]byte(stored.SpecJSON), &group); err != nil {
		return fmt.Sprintf("group %q: stored spec is not valid JSON: %v", gid, err)
	}
	group.Disabled = !enabled
	spec, err := json.Marshal(group)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	if err := s.server.deps.Groups.CreateGroup(ctx, gid, string(spec)); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	if enabled {
		return fmt.Sprintf("Successfully enabled Group id:%s", gid)
	}
	return fmt.Sprintf("Successfully disabled Group id:%s", gid)
}
