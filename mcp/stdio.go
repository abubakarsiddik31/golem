package mcp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// readPoll bounds one blocking slice of a stdio read while waiting
// for the next message; the loop re-checks the context between
// slices. Only in-flight calls poll — there is no idle reader.
const readPoll = 100 * time.Millisecond

// StdioConfig configures a server subprocess spoken to over its
// stdin and stdout, one JSON message per line.
type StdioConfig struct {
	// Command is the server executable; required.
	Command string
	// Args are the command's arguments.
	Args []string
	// Dir is the working directory for the server; empty means the
	// process's current directory.
	Dir string
	// Env lists extra environment entries in "KEY=VALUE" form,
	// appended after the process environment. Entries without "="
	// fail NewStdio.
	Env []string
}

// StdioTransport runs one MCP server as a subprocess and exchanges
// newline-delimited JSON-RPC messages over its stdin and stdout. It
// implements Transport and is not safe for concurrent use by itself —
// the Client serializes it.
type StdioTransport struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	out     *bufio.Reader
	// deadlineSetter is non-nil when the stdout pipe supports read
	// deadlines (pollable pipes); Windows pipes do not.
	deadlineSetter interface{ SetReadDeadline(time.Time) error }
}

var _ Transport = (*StdioTransport)(nil)

// NewStdio validates cfg, starts the server process, and returns a
// transport to it. The transport owns the process: Close terminates
// it. Server startup failures — a missing executable, a bad
// environment entry — surface here or on first use.
func NewStdio(cfg StdioConfig) (*StdioTransport, error) {
	if cfg.Command == "" {
		return nil, fmt.Errorf("mcp: command is required")
	}
	for _, entry := range cfg.Env {
		if !strings.Contains(entry, "=") {
			return nil, fmt.Errorf("mcp: env entry %q must be in KEY=VALUE form", entry)
		}
	}
	command := exec.Command(cfg.Command, cfg.Args...)
	command.Dir = cfg.Dir
	if cfg.Env != nil {
		command.Env = append(os.Environ(), cfg.Env...)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdin pipe: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdout pipe: %w", err)
	}
	command.Stderr = nil // server logs go nowhere; text channel only
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("mcp: start %s: %w", cfg.Command, err)
	}
	return &StdioTransport{
		command:        command,
		stdin:          stdin,
		out:            bufio.NewReaderSize(stdout, 64*1024),
		deadlineSetter: asDeadlineSetter(stdout),
	}, nil
}

// asDeadlineSetter reports stdout's deadline capability, if any.
func asDeadlineSetter(stdout io.Reader) interface{ SetReadDeadline(time.Time) error } {
	setter, _ := stdout.(interface{ SetReadDeadline(time.Time) error })
	return setter
}

// Send writes one message as a single line.
func (t *StdioTransport) Send(ctx context.Context, message []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := t.stdin.Write(append(message, '\n')); err != nil {
		return fmt.Errorf("write to server: %w", err)
	}
	return nil
}

// Read returns the next complete line the server wrote. Where the
// pipe supports deadlines, waiting is sliced into readPoll chunks so
// context cancellation is honored; a partial line interrupted by a
// deadline is buffered and completed on the next slice.
func (t *StdioTransport) Read(ctx context.Context) ([]byte, error) {
	var buffered []byte
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if t.deadlineSetter != nil {
			deadline := time.Now().Add(readPoll)
			if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
				deadline = d
			}
			if err := t.deadlineSetter.SetReadDeadline(deadline); err != nil {
				return nil, fmt.Errorf("mcp: set read deadline: %w", err)
			}
		}
		chunk, err := t.out.ReadString('\n')
		buffered = append(buffered, chunk...)
		if err == nil {
			return []byte(strings.TrimRight(string(buffered), "\r\n")), nil
		}
		if isTimeout(err) {
			continue
		}
		if len(buffered) > 0 && errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("mcp: server closed stdin mid-message")
		}
		return nil, fmt.Errorf("mcp: read from server: %w", err)
	}
}

// Close tears the server down: closing stdin, then killing the
// process and reaping it. Killing a process that already exited on
// stdin EOF is harmless. Teardown is best-effort; Close does not
// report the server's exit status.
func (t *StdioTransport) Close() error {
	t.stdin.Close()
	if t.command.Process != nil {
		// Ignore the error: the process may already have exited.
		t.command.Process.Kill()
	}
	_ = t.command.Wait()
	return nil
}

// isTimeout reports whether err is a file-descriptor timeout.
func isTimeout(err error) bool {
	return os.IsTimeout(err) || errors.Is(err, os.ErrDeadlineExceeded)
}
