package shell

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/abubakarsiddik31/golem/model"
)

// DestructiveCommands is the starting-point denylist for
// Config.DeniedCommands: commands whose ordinary effect is to destroy
// data or take the host down. It mirrors the destructive-command
// denylist other agent frameworks ship. Pass it as
//
//	shell.Config{DeniedCommands: shell.DestructiveCommands}
//
// and extend or replace it to taste. Denial is a guardrail against
// accidents, not a security boundary: the check inspects only the
// command's first token, and allowlisted or unlisted commands can
// still spawn others.
var DestructiveCommands = []string{
	"rm", "rmdir", "mkfs", "dd",
	"format", "shutdown", "reboot", "halt", "poweroff", "init",
}

// ChainingOperators is the starting-point denylist for
// Config.DeniedOperators: the shell operators that chain or redirect
// commands. Matching is a substring check on the whole command string,
// so quoting that mentions an operator in an argument is denied too —
// the check is deliberately conservative.
var ChainingOperators = []string{
	";", "|", "&", ">", "<", "`", "$(",
}

// CredentialEnvPatterns is the starting-point denylist for
// Config.DeniedEnvPatterns: glob patterns for environment variable
// names commonly carrying credentials. It targets a coarse common
// core, not every host secret; treat it as a starting point and add
// patterns of your own.
var CredentialEnvPatterns = []string{
	"*_API_KEY", "*_APIKEY", "*_TOKEN", "*_SECRET",
	"*_PASSWORD", "*_PASSWD", "*_CREDENTIAL*",
	"AWS_*", "AZURE_*",
}

// interactiveCommands are executables that ordinarily expect a
// terminal and would hang a tool call until the timeout. Blocked when
// Config.BlockInteractive is set unless the configuration allows the
// executable another way.
var interactiveCommands = []string{
	"vi", "vim", "nvim", "nano", "pico", "emacs",
	"top", "htop", "less", "more",
	"ssh", "sudo", "su", "telnet", "ftp", "gdb",
}

// policy holds the resolved command policy behind one tool's exec. The
// zero value filters nothing.
type policy struct {
	allowed          map[string]bool
	denied           map[string]bool
	deniedOperators  []string
	blockInteractive bool
}

// newPolicy validates and resolves the policy half of a Config. A
// command set as both allowlist and denylist is a configuration error;
// empty entries are configuration errors too, because a silent no-op
// entry would look like enforcement.
func newPolicy(allowed, denied, operators []string, blockInteractive bool) (*policy, error) {
	for _, list := range [][]string{allowed, denied, operators} {
		for _, entry := range list {
			if strings.TrimSpace(entry) == "" {
				return nil, fmt.Errorf("shell: policy entries must not be empty")
			}
		}
	}
	if len(allowed) > 0 && len(denied) > 0 {
		return nil, fmt.Errorf("shell: allowed and denied command lists are mutually exclusive; set one, not both")
	}
	p := &policy{
		allowed:          nameSet(allowed),
		denied:           nameSet(denied),
		deniedOperators:  operators,
		blockInteractive: blockInteractive,
	}
	return p, nil
}

// nameSet indexes command entries for lookup. An entry without a
// separator matches the command's base executable name; an entry with
// one matches the executable's full form.
func nameSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	return set
}

// check reports whether a model-produced command may run, as a
// correctable rejection when it may not: the run continues and the
// model can pick another command within the retry budget.
func (p *policy) check(command string) error {
	if p == nil {
		return nil
	}
	for _, op := range p.deniedOperators {
		if strings.Contains(command, op) {
			return &model.ModelRetry{Err: fmt.Errorf("command denied: shell operator %q is not allowed; run one simple command per call", op)}
		}
	}
	executable := executableOf(command)
	switch {
	case len(p.allowed) > 0:
		if !matchName(p.allowed, executable) {
			return &model.ModelRetry{Err: fmt.Errorf("command %q is not on the allowed list; use one of the allowed commands", executable)}
		}
	case len(p.denied) > 0:
		if matchName(p.denied, executable) {
			return &model.ModelRetry{Err: fmt.Errorf("command %q is denied by policy; choose a non-destructive alternative", executable)}
		}
	}
	if p.blockInteractive && matchName(nameSet(interactiveCommands), executable) {
		return &model.ModelRetry{Err: fmt.Errorf("command %q expects an interactive terminal; use a non-interactive form", executable)}
	}
	return nil
}

// executableOf extracts the command's executable token: the first
// whitespace-separated token with its surrounding quotes stripped.
// The command is interpreted by the platform shell, so this is
// best-effort by design — the guardrail checks the first token, and
// documentation says so.
func executableOf(command string) string {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return ""
	}
	token := fields[0]
	return strings.Trim(token, `"'`)
}

// matchName reports whether the executable matches any entry: by base
// name for entries without a separator, by exact form otherwise.
func matchName(set map[string]bool, executable string) bool {
	if set[executable] {
		return true
	}
	base := filepath.Base(executable)
	return base != executable && set[base]
}

// filterEnv drops entries whose KEY matches any denied glob pattern.
// Patterns apply to the merged environment — the process environment
// plus Config.Env — so credentials already in the process environment
// are filtered along with entries the application added.
func filterEnv(env []string, patterns []string) []string {
	if len(patterns) == 0 {
		return env
	}
	kept := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			kept = append(kept, entry)
			continue
		}
		blocked := false
		for _, pattern := range patterns {
			if matched, err := filepath.Match(pattern, key); err == nil && matched {
				blocked = true
				break
			}
		}
		if !blocked {
			kept = append(kept, entry)
		}
	}
	return kept
}
