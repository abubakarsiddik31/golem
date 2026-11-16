package mcpserver

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/abubakarsiddik31/golem/mcp"
)

// StdioTransport exchanges newline-delimited JSON-RPC messages over an
// input and output stream, the server side of MCP's stdio transport. It
// implements mcp.Transport and serializes writes; reads are single-reader.
type StdioTransport struct {
	in  *bufio.Reader
	out io.Writer
	mu  sync.Mutex
}

var _ mcp.Transport = (*StdioTransport)(nil)

// NewStdio wraps the server's stdin and stdout (or any equivalent
// streams) as an MCP Transport.
func NewStdio(in io.Reader, out io.Writer) *StdioTransport {
	return &StdioTransport{in: bufio.NewReaderSize(in, 64*1024), out: out}
}

// Read returns the next non-empty line, trimmed of its line ending.
func (t *StdioTransport) Read(ctx context.Context) ([]byte, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line, err := t.in.ReadString('\n')
		if err != nil {
			if len(line) > 0 && err == io.EOF {
				return []byte(strings.TrimRight(line, "\r\n")), nil
			}
			return nil, err
		}
		message := strings.TrimRight(line, "\r\n")
		if message != "" {
			return []byte(message), nil
		}
	}
}

// Send writes one message as a single line.
func (t *StdioTransport) Send(ctx context.Context, message []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, err := t.out.Write(append(message, '\n')); err != nil {
		return fmt.Errorf("mcpserver: write stdio response: %w", err)
	}
	return nil
}

// Close is a no-op: the application owns the streams.
func (t *StdioTransport) Close() error { return nil }
