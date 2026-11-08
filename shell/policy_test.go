package shell

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/abubakarsiddik31/golem/model"
)

// rawArgs marshals one tool-call argument object.
func rawArgs(t *testing.T, command string) json.RawMessage {
	t.Helper()
	args, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return args
}

// wantRetry asserts the error is a correctable *model.ModelRetry whose
// message mentions the given fragment.
func wantRetry(t *testing.T, err error, fragment string) {
	t.Helper()
	var retry *model.ModelRetry
	if !errors.As(err, &retry) {
		t.Fatalf("check error: want *model.ModelRetry, got %v", err)
	}
	if fragment != "" && !strings.Contains(retry.Error(), fragment) {
		t.Fatalf("check error %q: want fragment %q", retry.Error(), fragment)
	}
}

func TestPolicyAllowlist(t *testing.T) {
	p, err := newPolicy([]string{"ls", "cat", "rg"}, nil, nil, false)
	if err != nil {
		t.Fatalf("newPolicy: %v", err)
	}
	if err := p.check("ls -la /tmp"); err != nil {
		t.Fatalf("allowed command: %v", err)
	}
	// An entry matches the executable's base name: /bin/ls matches "ls".
	if err := p.check("/bin/ls -la"); err != nil {
		t.Fatalf("allowed by base name: %v", err)
	}
	wantRetry(t, p.check("git status"), "not on the allowed list")
	wantRetry(t, p.check("'git' status"), "not on the allowed list")
	// Arguments must not bypass the first-token check.
	wantRetry(t, p.check("echo git"), "not on the allowed list")
}

func TestPolicyDenylist(t *testing.T) {
	p, err := newPolicy(nil, DestructiveCommands, nil, false)
	if err != nil {
		t.Fatalf("newPolicy: %v", err)
	}
	wantRetry(t, p.check("rm -rf /tmp/x"), "denied by policy")
	wantRetry(t, p.check("/bin/rm -rf /tmp/x"), "denied by policy")
	if err := p.check("chmod +x build.sh"); err != nil {
		t.Fatalf("non-destructive command: %v", err)
	}
}

func TestPolicyOperators(t *testing.T) {
	p, err := newPolicy(nil, nil, ChainingOperators, false)
	if err != nil {
		t.Fatalf("newPolicy: %v", err)
	}
	if err := p.check("echo hello"); err != nil {
		t.Fatalf("plain command: %v", err)
	}
	for _, command := range []string{
		"echo hi > /tmp/out",
		"cat /etc/passwd | wc -l",
		"echo a && echo b",
		"echo a ; echo b",
		"echo `whoami`",
		"echo $(date)",
	} {
		if err := p.check(command); err != nil {
			continue
		}
		t.Errorf("chaining command %q passed the operator check", command)
	}
	// Quoting that mentions an operator is denied too — documented as
	// conservative.
	if err := p.check("grep '|' /tmp/file"); err == nil {
		t.Error("quoted operator passed the operator check")
	}
}

func TestPolicyInteractive(t *testing.T) {
	p, err := newPolicy(nil, nil, nil, true)
	if err != nil {
		t.Fatalf("newPolicy: %v", err)
	}
	wantRetry(t, p.check("vim /tmp/file"), "interactive terminal")
	wantRetry(t, p.check("sudo apt install jq"), "interactive terminal")
	if err := p.check("echo hello"); err != nil {
		t.Fatalf("plain command: %v", err)
	}

	open, err := newPolicy(nil, nil, nil, false)
	if err != nil {
		t.Fatalf("newPolicy: %v", err)
	}
	if err := open.check("sudo ls"); err != nil {
		t.Fatalf("BlockInteractive unset should allow any command: %v", err)
	}
}

func TestPolicyZeroValueFiltersNothing(t *testing.T) {
	p, err := newPolicy(nil, nil, nil, false)
	if err != nil {
		t.Fatalf("newPolicy: %v", err)
	}
	for _, command := range []string{"rm -rf /", "sudo reboot", "echo a | b"} {
		if err := p.check(command); err != nil {
			t.Errorf("zero policy denied %q: %v", command, err)
		}
	}
}

func TestNewPolicyValidation(t *testing.T) {
	if _, err := newPolicy([]string{"ls"}, []string{"rm"}, nil, false); err == nil {
		t.Error("allowlist + denylist: want error, got nil")
	}
	for _, bad := range [][]string{{"ls", ""}, {""}, {" "}} {
		if _, err := newPolicy(bad, nil, nil, false); err == nil {
			t.Errorf("entries %q: want error, got nil", bad)
		}
	}
}

func TestPolicyOrderOperatorsBeforeLists(t *testing.T) {
	// An operator denial wins over an allowlist miss so the message
	// always points at the closest correction.
	p, err := newPolicy([]string{"echo"}, nil, []string{"|"}, false)
	if err != nil {
		t.Fatalf("newPolicy: %v", err)
	}
	wantRetry(t, p.check("grep a | b"), "operator")
}

func TestExecutableOf(t *testing.T) {
	cases := map[string]string{
		"  ls -la  ":             "ls",
		`"git" status`:           "git",
		"'git' status":           "git",
		"/bin/ls -la":            "/bin/ls",
		`C:\Windows\notepad.exe`: `C:\Windows\notepad.exe`,
	}
	for command, want := range cases {
		if got := executableOf(command); got != want {
			t.Errorf("executableOf(%q) = %q, want %q", command, got, want)
		}
	}
}

func TestFilterEnv(t *testing.T) {
	processEnv := []string{
		"PATH=/usr/bin",
		"OPENAI_API_KEY=sk-x",
		"GITHUB_TOKEN=gh-x",
		"DB_PASSWORD=secret",
		"HOME=/home/u",
		"NO_EQUALS",
	}
	got := filterEnv(processEnv, CredentialEnvPatterns)
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "OPENAI_API_KEY") || strings.Contains(joined, "GITHUB_TOKEN") || strings.Contains(joined, "DB_PASSWORD") {
		t.Errorf("filterEnv kept a credential: %s", joined)
	}
	for _, keep := range []string{"PATH=", "HOME=", "NO_EQUALS"} {
		if !strings.Contains(joined, keep) {
			t.Errorf("filterEnv dropped %s: %s", keep, joined)
		}
	}
	if noPatterns := filterEnv(processEnv, nil); len(noPatterns) != len(processEnv) {
		t.Errorf("filterEnv with no patterns dropped entries")
	}
}

func TestRunDeniedCommandIsRetryable(t *testing.T) {
	dir := t.TempDir()
	tool, err := New[struct{}](Config{
		Dir:               dir,
		DeniedCommands:    DestructiveCommands,
		DeniedEnvPatterns: []string{"*_TOKEN"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// The denial happens before exec: no process runs, and the error is
	// the correctable rejection the run loop feeds back to the model.
	_, err = tool.Exec(context.Background(), struct{}{}, rawArgs(t, "rm -rf /tmp/anything"))
	wantRetry(t, err, "denied by policy")

	// An allowed command still runs and still sees a filtered
	// environment.
	tool2, err := New[struct{}](Config{
		Dir:               dir,
		Env:               []string{"ALLOWED_TOKEN=abc", "PLAIN=1"},
		DeniedEnvPatterns: []string{"*_TOKEN"},
		Timeout:           10 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := tool2.Exec(context.Background(), struct{}{}, rawArgs(t, "echo env probe: $ALLOWED_TOKEN"))
	if err != nil {
		t.Fatalf("allowed command: %v", err)
	}
	if strings.Contains(result.Text, "abc") {
		t.Errorf("denied env pattern leaked into the command environment: %s", result.Text)
	}
}

func TestRunOperatorDeniedBeforeExec(t *testing.T) {
	dir := t.TempDir()
	tool, err := New[struct{}](Config{
		Dir:             dir,
		DeniedOperators: []string{";"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = tool.Exec(context.Background(), struct{}{}, rawArgs(t, "echo a; echo b"))
	wantRetry(t, err, "operator")
}
