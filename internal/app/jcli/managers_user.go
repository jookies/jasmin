package jcli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/pumpitspace/jasmin/internal/app/admin"
	"github.com/pumpitspace/jasmin/internal/app/outbound"
	"github.com/pumpitspace/jasmin/internal/app/smppsserver"
)

// User management (J-004, J-005). Literals come from
// spec/compatibility/fixtures/jcli/J-004-user.jsonl and J-005-*.jsonl.

var (
	legacyUserID   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,16}$`)
	legacyUsername = regexp.MustCompile(`^[A-Za-z0-9_-]{1,15}$`)
)

// userKind is the interactive contract for `user -a` / `user -u`.
var userKind = &entityKind{
	keyLabel:        "User",
	addAnnouncement: "Adding a new User: (ok: save, ko: exit)",
	updateAnnouncement: func(uid string) string {
		return fmt.Sprintf("Updating User id [%s]: (ok: save, ko: exit)", uid)
	},
	required: []string{"gid", "password", "uid", "username"},
	validate: validateUserKey,
	save:     saveUser,
}

// validateUserKey accepts one `key value` line of a user session. The credential
// keys take three words ("mt_messaging_cred quota balance 10"), so the section
// and key are parsed out of the argument.
func validateUserKey(is *interactiveSession, key, argument string) (string, bool) {
	switch key {
	case "uid":
		if !legacyUserID.MatchString(argument) {
			return "Error: User uid syntax is invalid", false
		}
	case "username":
		if is.updating {
			// Proven by J-004: the oracle refuses, keeps the session open, and
			// does not record the value.
			return "User username can not be modified !", false
		}
		if !legacyUsername.MatchString(argument) {
			return "Error: User username syntax is invalid", false
		}
	case "gid":
		if !legacyGroupID.MatchString(argument) {
			return "Error: Group gid syntax is invalid", false
		}
	case "password":
		// No syntax constraint in the oracle beyond being present.
	case "mt_messaging_cred", "smpps_cred":
		return validateCredentialKey(is, key, argument)
	default:
		return fmt.Sprintf("Unknown User key: %s", key), false
	}
	is.set(key, argument)
	return "", true
}

// credentialSyntax splits "section key value" out of a credential argument.
var credentialSyntax = regexp.MustCompile(`^(\S+) (\S+) (\S+.*)$`)

func validateCredentialKey(is *interactiveSession, command, argument string) (string, bool) {
	match := credentialSyntax.FindStringSubmatch(strings.TrimSpace(argument))
	if match == nil {
		return fmt.Sprintf("Error: expected syntax: %s section key value", command), false
	}
	section, key, value := strings.ToLower(match[1]), strings.ToLower(match[2]), match[3]

	if command == "smpps_cred" {
		switch {
		case section == "authorization" && (key == "bind" || key == "ip"):
		case section == "quota" && key == "max_bindings":
		default:
			return fmt.Sprintf("Error: invalid section name: %s", section), false
		}
	} else {
		switch section {
		case "authorization":
			if consoleAuthorizationPointer(&outbound.MTCredentialConfig{}, key) == nil &&
				!validAuthorizationKey(key) {
				return fmt.Sprintf("Error: invalid key: %s, possible keys: %s",
					key, strings.Join(mtAuthorizationOrder, ", ")), false
			}
		case "valuefilter":
			if !contains(mtValueFilterOrder, key) {
				return fmt.Sprintf("Error: invalid key: %s, possible keys: %s",
					key, strings.Join(mtValueFilterOrder, ", ")), false
			}
			if _, err := regexp.Compile(value); err != nil {
				return fmt.Sprintf("Error: %v", err), false
			}
		case "defaultvalue":
			if key != "src_addr" {
				return fmt.Sprintf("Error: invalid key: %s, possible keys: src_addr", key), false
			}
		case "quota":
			if message, ok := validateQuotaValue(key, value); !ok {
				return message, false
			}
		default:
			return fmt.Sprintf("Error: invalid section name: %s", section), false
		}
	}

	is.set(command+" "+section+" "+key, value)
	return "", true
}

// validateQuotaValue enforces the numeric casts the oracle performs, including
// the Python error text a bad float produces.
func validateQuotaValue(key, value string) (string, bool) {
	switch key {
	case "balance", "early_percent", "http_throughput", "smpps_throughput":
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return fmt.Sprintf("Error: could not convert string to float: '%s'", value), false
		}
	case "sms_count":
		if _, err := strconv.Atoi(value); err != nil {
			return fmt.Sprintf("Error: invalid literal for int() with base 10: '%s'", value), false
		}
	default:
		return fmt.Sprintf("Error: invalid key: %s, possible keys: balance, early_percent, sms_count, http_throughput, smpps_throughput", key), false
	}
	return "", true
}

func validAuthorizationKey(key string) bool { return contains(mtAuthorizationOrder, key) }

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// saveUser materialises the collected keys into an outbound user, mirrors the
// SMPPs half into the bind-account service, and reports the oracle's message.
func saveUser(s *session, is *interactiveSession) (string, bool) {
	ctx, cancel := s.context()
	defer cancel()

	var base outbound.UserConfig
	uid := is.values["uid"]
	if is.updating {
		stored, resolved, err := s.findUserByUID(ctx, is.targetID)
		if err != nil {
			return fmt.Sprintf("Unknown User: %s", is.targetID), false
		}
		base = resolved
		uid = is.targetID
		_ = stored
		if len(is.values) == 0 {
			// The oracle reports this and keeps the session open.
			return "Nothing to save", false
		}
	}

	user, message, ok := applyUserValues(base, uid, is)
	if !ok {
		return message, false
	}

	spec, err := json.Marshal(user)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), false
	}
	if err := s.server.deps.Users.CreateUser(ctx, user.Username, string(spec)); err != nil {
		return fmt.Sprintf("Error: %v", err), false
	}
	if message, ok := s.mirrorSMPPsAccount(ctx, user, is.values["password"]); !ok {
		return message, false
	}

	if is.updating {
		return fmt.Sprintf("Successfully updated User [%s]", uid), true
	}
	return fmt.Sprintf("Successfully added User [%s] to Group [%s]", uid, user.GroupID), true
}

// applyUserValues folds the session's key/value pairs onto a base user.
func applyUserValues(user outbound.UserConfig, uid string, is *interactiveSession) (outbound.UserConfig, string, bool) {
	user.ExternalID = uid
	for _, key := range is.typed {
		value := is.values[key]
		switch {
		case key == "username":
			user.Username = value
		case key == "password":
			user.PasswordSHA256 = sha256Hex(value)
		case key == "gid":
			user.GroupID = value
		case key == "uid":
			user.ExternalID = value
		case strings.HasPrefix(key, "mt_messaging_cred "), strings.HasPrefix(key, "smpps_cred "):
			if message, ok := applyCredentialValue(&user, key, value); !ok {
				return user, message, false
			}
		}
	}
	return user, "", true
}

func applyCredentialValue(user *outbound.UserConfig, key, value string) (string, bool) {
	parts := strings.Fields(key)
	if len(parts) != 3 {
		return fmt.Sprintf("Error: malformed credential key %q", key), false
	}
	command, section, name := parts[0], parts[1], parts[2]

	if command == "smpps_cred" {
		if user.SMPPSCredential == nil {
			user.SMPPSCredential = &outbound.SMPPSCredentialConfig{}
		}
		switch {
		case section == "authorization" && name == "bind":
			parsed := parsePythonBool(value)
			user.SMPPSCredential.Bind = &parsed
		case section == "authorization" && name == "ip":
			user.SMPPSCredential.IP = value
		case section == "quota" && name == "max_bindings":
			count, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Sprintf("Error: invalid literal for int() with base 10: '%s'", value), false
			}
			user.SMPPSCredential.MaxBindings = &count
		}
		return "", true
	}

	if user.MTCredential == nil {
		user.MTCredential = &outbound.MTCredentialConfig{}
	}
	switch section {
	case "authorization":
		setConsoleAuthorization(user.MTCredential, name, parsePythonBool(value))
	case "valuefilter":
		setValueFilter(user.MTCredential, name, value)
	case "defaultvalue":
		copied := value
		user.MTCredential.DefaultSourceAddress = &copied
	case "quota":
		switch name {
		case "balance":
			parsed, _ := strconv.ParseFloat(value, 64)
			user.Balance = &parsed
		case "sms_count":
			parsed, _ := strconv.Atoi(value)
			user.SubmitSMCount = &parsed
		case "early_percent":
			parsed, _ := strconv.ParseFloat(value, 64)
			percent := int(parsed)
			user.EarlyDecrementBalancePercent = &percent
		case "http_throughput":
			parsed, _ := strconv.ParseFloat(value, 64)
			user.MTCredential.HTTPThroughput = &parsed
		case "smpps_throughput":
			parsed, _ := strconv.ParseFloat(value, 64)
			user.MTCredential.SMPPSThroughput = &parsed
		}
	}
	return "", true
}

// sha256Hex is how the outbound user config carries a password: the directory
// compares this digest, so the console never stores or echoes the plaintext.
func sha256Hex(password string) string {
	digest := sha256.Sum256([]byte(password))
	return hex.EncodeToString(digest[:])
}

// parsePythonBool accepts the same words the oracle casts to a bool
// (usersm.py TrueBoolCastMap / FalseBoolCastMap).
func parsePythonBool(value string) bool {
	switch strings.ToLower(value) {
	case "true", "1", "t", "y", "yes":
		return true
	default:
		return false
	}
}

// mirrorSMPPsAccount keeps the SMPPs bind account in step with the user's
// smpps_cred half. In legacy one record serves both protocols; the Go model
// splits them, so provisioning a user through the console must touch both or
// the console would report a bind authorization nothing enforces.
//
// plaintext is the password as typed, empty on the paths that never see it
// (enable/disable/ban). The bind account stores the password in the clear
// because smppsserver hashes it itself -- writing the already-hashed value
// would have the server hash it twice, and every bind would fail
// ESME_RINVPASWD with nothing in the transcript to explain why.
func (s *session) mirrorSMPPsAccount(ctx context.Context, user outbound.UserConfig, plaintext string) (string, bool) {
	if s.server.deps.SMPPsUsers == nil || user.SMPPSCredential == nil {
		return "", true
	}
	if plaintext == "" {
		// Preserve whatever password the account already carries rather than
		// clobbering it with a value this path does not have.
		if existing, err := s.server.deps.SMPPsUsers.GetUser(ctx, user.Username); err == nil {
			var account smppsserver.UserConfig
			if json.Unmarshal([]byte(existing.SpecJSON), &account) == nil {
				plaintext = account.Password
			}
		}
	}
	if plaintext == "" {
		// Nothing to bind with; do not create a half-formed account.
		return "", true
	}
	account := smppsserver.UserConfig{
		SystemID:    user.Username,
		Password:    plaintext,
		Disabled:    user.Disabled,
		MaxBindings: user.SMPPSCredential.MaxBindings,
		SMPPSSend:   user.SMPPSCredential.Bind,
	}
	if user.SMPPSCredential.IP != "" {
		account.IPWhitelist = user.SMPPSCredential.IP
	}
	spec, err := json.Marshal(account)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), false
	}
	if err := s.server.deps.SMPPsUsers.PutUser(ctx, user.Username, string(spec)); err != nil {
		return fmt.Sprintf("Error: %v", err), false
	}
	return "", true
}

// findUserByUID resolves a legacy uid to its stored user. The Go store keys
// users by username; the legacy console keys them by uid, which lives in the
// spec as ExternalID.
func (s *session) findUserByUID(ctx context.Context, uid string) (admin.StoredUser, outbound.UserConfig, error) {
	stored, err := s.server.deps.Users.ListUsers(ctx)
	if err != nil {
		return admin.StoredUser{}, outbound.UserConfig{}, err
	}
	for _, entry := range stored {
		var user outbound.UserConfig
		if err := json.Unmarshal([]byte(entry.SpecJSON), &user); err != nil {
			continue
		}
		if user.ExternalID == uid {
			return entry, user, nil
		}
	}
	return admin.StoredUser{}, outbound.UserConfig{}, fmt.Errorf("jcli: unknown user uid %q", uid)
}
