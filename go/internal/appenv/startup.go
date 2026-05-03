package appenv

import "strings"

// StartupContext mirrors NzbDrone.Common.EnvironmentInfo.StartupContext
// — a minimal arg parser that recognises the loose `key=value` /
// `flag` argument style Sonarr's launcher scripts emit.
//
// Args may be prefixed with any of `/`, `-`, or `--` (the C# code
// trims `' '`, `'/'`, `'-'`); they may carry a `=value` suffix or
// stand alone as boolean flags. Keys are lowercased. A `=value`
// suffix is unquoted (`key="some path"` → `key`/`some path`).
//
// The well-known argument names live as `StartupArg*` constants so
// downstream packages reference them through the type system rather
// than re-typing strings.
type StartupContext struct {
	// Args holds key=value arguments. Keys are lowercased.
	Args map[string]string
	// Flags holds boolean flags (no `=value`). Lowercased.
	Flags map[string]struct{}
}

// Well-known startup argument and flag names. Mirrors the C# constants
// on StartupContext. Add only entries actually consumed by the Go
// host as we build it out — the full C# list is documented in
// analysis/subsystems/system.md.
const (
	// StartupArgData overrides the AppFolderInfo.AppData path
	// (`/data=/path/to/dir` or `--data=/path/to/dir`).
	StartupArgData = "data"
	// StartupFlagNoBrowser disables the system-browser launch on
	// startup. Matches `LaunchBrowser=false` semantics but takes
	// precedence — the C# code reads this at the host level before
	// IConfigFileProvider has loaded.
	StartupFlagNoBrowser = "nobrowser"
	// StartupFlagHelp prints usage and exits.
	StartupFlagHelp = "?"
	// StartupFlagTerminateExisting tells the host to kill an existing
	// Sonarr process holding the config dir before starting.
	StartupFlagTerminateExisting = "terminateexisting"
)

// ParseStartupArgs returns a StartupContext from the given argv-style
// slice. Each entry may be `key=value`, `key` (flag), or empty.
// Leading `/`, `-`, `--`, and surrounding whitespace are stripped
// from keys; key matching is case-insensitive (lowercased).
//
// Unknown / future keys are still captured — the parser does not
// reject them — so downstream code that introspects new flags doesn't
// require an update here.
func ParseStartupArgs(argv []string) *StartupContext {
	ctx := &StartupContext{
		Args:  map[string]string{},
		Flags: map[string]struct{}{},
	}
	for _, raw := range argv {
		s := strings.TrimSpace(raw)
		s = strings.TrimLeft(s, "-/")
		if s == "" {
			continue
		}
		if i := strings.Index(s, "="); i >= 0 {
			key := strings.ToLower(strings.TrimSpace(s[:i]))
			value := strings.TrimSpace(s[i+1:])
			value = strings.Trim(value, `"' `)
			if key != "" {
				ctx.Args[key] = value
			}
			continue
		}
		ctx.Flags[strings.ToLower(s)] = struct{}{}
	}
	return ctx
}

// HasFlag reports whether name is set as a boolean flag. Case-insensitive.
func (c *StartupContext) HasFlag(name string) bool {
	_, ok := c.Flags[strings.ToLower(name)]
	return ok
}

// Arg returns the value for a key=value argument and whether it was
// set. Case-insensitive on the key.
func (c *StartupContext) Arg(name string) (string, bool) {
	v, ok := c.Args[strings.ToLower(name)]
	return v, ok
}
