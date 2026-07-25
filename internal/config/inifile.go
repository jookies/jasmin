// Package config ports Jasmin's ConfigFile (jasmin/config/__init__.py): a
// RawConfigParser-style INI reader with typed getters, per-key defaults, and
// environment-variable overrides. It reproduces the legacy semantics and
// quirks exactly so existing jasmin.cfg files parse identically (C-001..C-016,
// C-015 env overrides).
package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// File is a parsed INI config. Section names are case-sensitive; option names
// are lowercased on parse (RawConfigParser's default optionxform), so lookups
// are case-insensitive in the option.
type File struct {
	sections map[string]map[string]string
	getenv   func(string) (string, bool) // injectable for tests; defaults to os.LookupEnv
}

// Parse reads INI text into a File. It mirrors RawConfigParser: `[section]`
// headers, `key = value` or `key: value` entries, full-line comments starting
// with '#' or ';' (inline comments are NOT stripped), leading/trailing value
// whitespace trimmed, and a duplicate option within a section is an error.
func Parse(reader io.Reader) (*File, error) {
	file := &File{sections: make(map[string]map[string]string), getenv: os.LookupEnv}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var currentSection string
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		raw := scanner.Text()
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || trimmed[0] == '#' || trimmed[0] == ';' {
			continue
		}
		if trimmed[0] == '[' {
			if !strings.HasSuffix(trimmed, "]") || len(trimmed) < 3 {
				return nil, fmt.Errorf("config: line %d: malformed section header %q", lineNumber, trimmed)
			}
			currentSection = trimmed[1 : len(trimmed)-1]
			if _, exists := file.sections[currentSection]; !exists {
				file.sections[currentSection] = make(map[string]string)
			}
			continue
		}
		if currentSection == "" {
			return nil, fmt.Errorf("config: line %d: option %q outside any section", lineNumber, trimmed)
		}
		separator := strings.IndexAny(raw, "=:")
		if separator < 0 {
			return nil, fmt.Errorf("config: line %d: no '=' or ':' in %q", lineNumber, trimmed)
		}
		option := strings.ToLower(strings.TrimSpace(raw[:separator]))
		if option == "" {
			return nil, fmt.Errorf("config: line %d: empty option name", lineNumber)
		}
		value := strings.TrimSpace(raw[separator+1:])
		if _, duplicate := file.sections[currentSection][option]; duplicate {
			return nil, fmt.Errorf("config: line %d: duplicate option %q in section %q", lineNumber, option, currentSection)
		}
		file.sections[currentSection][option] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("config: read: %w", err)
	}
	return file, nil
}

// ParseString is a convenience wrapper over Parse.
func ParseString(text string) (*File, error) {
	return Parse(strings.NewReader(text))
}

// envName maps a (section, option) to its override variable name:
// "SECTION_OPTION" uppercased with dashes turned into underscores.
func envName(section, option string) string {
	return strings.ToUpper(strings.ReplaceAll(section+"_"+option, "-", "_"))
}

// rawValue returns the config value for section.option, or ("", false) when
// absent. Env overrides are handled by the typed getters, not here.
func (f *File) rawValue(section, option string) (string, bool) {
	options, ok := f.sections[section]
	if !ok {
		return "", false
	}
	value, ok := options[strings.ToLower(option)]
	return value, ok
}

// Get returns section.option as a string: an environment override wins, then
// the config value, then the default. A config value of the literal "None"
// yields "" (the legacy _get returns Python None, which these callers treat as
// unset).
func (f *File) Get(section, option, def string) string {
	if value, ok := f.getenv(envName(section, option)); ok {
		return value
	}
	value, ok := f.rawValue(section, option)
	if !ok {
		return def
	}
	if value == "None" {
		return ""
	}
	return value
}

// GetInt returns section.option as an int. Order: env override, config value,
// default. A present value that does not parse as an int is an error (the
// legacy _getint raises). A config value of "None" yields the default (the
// legacy quirk: _getint maps "None" to the default, unlike _get).
func (f *File) GetInt(section, option string, def int) (int, error) {
	if value, ok := f.getenv(envName(section, option)); ok {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return 0, fmt.Errorf("config: env %s: %q is not an integer", envName(section, option), value)
		}
		return parsed, nil
	}
	value, ok := f.rawValue(section, option)
	if !ok || value == "None" {
		return def, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("config: [%s] %s: %q is not an integer", section, option, value)
	}
	return parsed, nil
}

// GetFloat mirrors GetInt for floats.
func (f *File) GetFloat(section, option string, def float64) (float64, error) {
	if value, ok := f.getenv(envName(section, option)); ok {
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return 0, fmt.Errorf("config: env %s: %q is not a number", envName(section, option), value)
		}
		return parsed, nil
	}
	value, ok := f.rawValue(section, option)
	if !ok || value == "None" {
		return def, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("config: [%s] %s: %q is not a number", section, option, value)
	}
	return parsed, nil
}

// GetBool returns section.option as a bool. Order: env override, config value,
// default. The two sources parse differently, matching the legacy code: an env
// override uses the loose _convert_to_bool (t/true/yes/y/1 → true, everything
// else → false), while a config value uses RawConfigParser.getboolean's strict
// set (1/yes/true/on → true, 0/no/false/off → false, anything else an error).
func (f *File) GetBool(section, option string, def bool) (bool, error) {
	if value, ok := f.getenv(envName(section, option)); ok {
		return looseBool(value), nil
	}
	value, ok := f.rawValue(section, option)
	if !ok {
		return def, nil
	}
	switch strings.ToLower(value) {
	case "1", "yes", "true", "on":
		return true, nil
	case "0", "no", "false", "off":
		return false, nil
	default:
		return false, fmt.Errorf("config: [%s] %s: %q is not a boolean", section, option, value)
	}
}

// looseBool is the legacy _convert_to_bool used for environment overrides.
func looseBool(value string) bool {
	switch strings.ToLower(value) {
	case "t", "true", "yes", "y", "1":
		return true
	default:
		return false
	}
}

// HasSection reports whether a section is present.
func (f *File) HasSection(section string) bool {
	_, ok := f.sections[section]
	return ok
}

// Lookup exposes the File's environment access for top-level override variables
// (e.g. CLOUDAMQP_URL, REDIS_URL) that the legacy config classes read directly
// rather than through a section. It uses the same injectable source as the
// typed getters, so tests can drive it deterministically.
func (f *File) Lookup(name string) (string, bool) {
	return f.getenv(name)
}
