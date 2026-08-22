// Package shell provides a common tool that runs one shell command and
// returns its combined output for the model.
//
// The command string is model-produced and executes with this
// process's privileges: the tool is strictly opt-in, and applications
// register it only where that is acceptable, scoping what a command can
// touch with the working directory, the environment, the timeout, and
// OS-level isolation of their choosing.
//
// The tool is an ordinary tool.Tool[Deps]: it composes with any agent
// dependency type and every agent option. Its name, description, and
// argument schema are public contract — models and prompts depend on
// them staying stable.
package shell

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

// ToolName is the name of the tool New returns.
const ToolName = "run_command"

// ToolDescription is the description the model sees for the tool.
const ToolDescription = "Run one shell command and return its combined stdout and stderr. A non-zero exit status is reported at the end of the output, not as a failure."

const (
	// DefaultTimeout bounds one command when Config.Timeout is zero.
	DefaultTimeout = 60 * time.Second
	// DefaultMaxBytes caps combined stdout and stderr when
	// Config.MaxBytes is zero.
	DefaultMaxBytes int64 = 1 << 20 // 1 MiB
)

// schema describes the tool's single command argument; it is public
// contract.
var schema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"command": {
			"type": "string",
			"description": "The shell command to run, as it would be typed at a prompt."
		}
	},
	"required": ["command"],
	"additionalProperties": false
}`)

// Config configures the command tool. The zero value is ready: it runs
// in the process working directory with the process environment, a
// DefaultTimeout bound, and DefaultMaxBytes of output.
type Config struct {
	// Dir is the working directory for the command; empty means the
	// process's current directory. If set it must exist and be a
	// directory.
	Dir string
	// Env lists extra environment entries in "KEY=VALUE" form, appended
	// after the process environment. Entries without "=" fail New.
	Env []string
	// Timeout bounds one command with a context deadline; zero selects
	// DefaultTimeout. Negative values fail New.
	Timeout time.Duration
	// MaxBytes caps bytes kept from combined stdout and stderr; the
	// kept output ends with a truncation marker rather than failing.
	// Zero selects DefaultMaxBytes; negative values fail New.
	MaxBytes int64
}

// runner holds the resolved configuration behind the tool's exec.
type runner struct {
	dir      string
	env      []string
	timeout  time.Duration
	maxBytes int64
}

// New validates cfg and returns the run_command tool ready for
// registration with an agent. Deps is the agent's dependency type; the
// run itself does not use it, but the tool carries it so one
// constructor serves every agent.
func New[Deps any](cfg Config) (tool.Tool[Deps], error) {
	if cfg.Timeout < 0 {
		return tool.Tool[Deps]{}, fmt.Errorf("shell: timeout must not be negative, got %s", cfg.Timeout)
	}
	if cfg.MaxBytes < 0 {
		return tool.Tool[Deps]{}, fmt.Errorf("shell: max bytes must not be negative, got %d", cfg.MaxBytes)
	}
	for _, entry := range cfg.Env {
		if !strings.Contains(entry, "=") {
			return tool.Tool[Deps]{}, fmt.Errorf("shell: env entry %q must be in KEY=VALUE form", entry)
		}
	}
	if cfg.Dir != "" {
		info, err := os.Stat(cfg.Dir)
		if err != nil {
			return tool.Tool[Deps]{}, fmt.Errorf("shell: dir %s: %w", cfg.Dir, err)
		}
		if !info.IsDir() {
			return tool.Tool[Deps]{}, fmt.Errorf("shell: dir %s is not a directory", cfg.Dir)
		}
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	maxBytes := cfg.MaxBytes
	if maxBytes == 0 {
		maxBytes = DefaultMaxBytes
	}
	r := &runner{
		dir:      cfg.Dir,
		env:      cfg.Env,
		timeout:  timeout,
		maxBytes: maxBytes,
	}
	return tool.New(tool.Tool[Deps]{
		Name:        ToolName,
		Description: ToolDescription,
		Schema:      schema,
		Exec: func(ctx context.Context, deps Deps, args json.RawMessage) (string, error) {
			return r.run(ctx, args)
		},
	})
}

// MustNew is New for tools declared as package-level values; it panics
// on invalid configuration.
func MustNew[Deps any](cfg Config) tool.Tool[Deps] {
	validated, err := New[Deps](cfg)
	if err != nil {
		panic(err)
	}
	return validated
}

// run runs one run_command call: validate the model-produced command
// as correctable arguments, execute it, and report combined output. A
// non-zero exit is evidence for the model, not a tool failure.
func (r *runner) run(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	command, err := parseArgs(args)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, shellPath(), shellFlag(), command)
	cmd.Dir = r.dir
	if r.env != nil {
		cmd.Env = append(os.Environ(), r.env...)
	}
	output := newCappedBuffer(int(r.maxBytes))
	cmd.Stdout = output
	cmd.Stderr = output
	runErr := cmd.Run()

	// A deadline or cancellation kills the process; report the context
	// error, never the exit it caused.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", fmt.Errorf("shell: run %q: %w", command, ctxErr)
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			return "", fmt.Errorf("shell: run %q: %w", command, runErr)
		}
		return finish(output.String(), fmt.Sprintf("[shell: exited with status %d]", exitErr.ExitCode())), nil
	}
	return finish(output.String(), ""), nil
}

// parseArgs extracts the command argument. Malformed arguments are
// model mistakes, so they reject as correctable *model.ModelRetry.
func parseArgs(args json.RawMessage) (string, error) {
	var input struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return "", &model.ModelRetry{Err: fmt.Errorf("arguments must be an object with a string command: %w", err)}
	}
	if input.Command == "" {
		return "", &model.ModelRetry{Err: fmt.Errorf("command is required")}
	}
	return input.Command, nil
}

// finish appends a trailing note (an exit status or truncation marker)
// to the output, separated by a blank line when there is output.
func finish(output string, note string) string {
	if note == "" {
		return output
	}
	if output != "" {
		output += "\n\n"
	}
	return output + note
}

// shellPath and shellFlag select the platform command interpreter.
func shellPath() string {
	if runtime.GOOS == "windows" {
		return "cmd"
	}
	return "sh"
}

func shellFlag() string {
	if runtime.GOOS == "windows" {
		return "/C"
	}
	return "-c"
}

// cappedBuffer keeps the first limit bytes written to it, flags the
// rest as truncated, and always claims full writes so the command's
// own writes never fail mid-run.
type cappedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func newCappedBuffer(limit int) *cappedBuffer {
	return &cappedBuffer{limit: limit}
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.buf.Len(); room > 0 {
		if len(p) <= room {
			b.buf.Write(p)
			return len(p), nil
		}
		b.buf.Write(p[:room])
	}
	b.truncated = true
	return len(p), nil
}

// String returns the kept output plus a truncation marker when bytes
// were dropped.
func (b *cappedBuffer) String() string {
	output := b.buf.String()
	if b.truncated {
		output = finish(output, fmt.Sprintf("[shell: output truncated at %d bytes]", b.limit))
	}
	return output
}
