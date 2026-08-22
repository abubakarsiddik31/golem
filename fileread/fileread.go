// Package fileread provides a common tool that reads a file inside a
// configured root directory and returns its text for the model. Paths
// are model-produced and untrusted: the tool confines every read to the
// root, rejecting absolute paths, traversal, and symlinks that escape.
//
// The tool is an ordinary tool.Tool[Deps]: it composes with any agent
// dependency type and every agent option. Its name, description, and
// argument schema are public contract — models and prompts depend on
// them staying stable.
package fileread

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

// ToolName is the name of the tool New returns.
const ToolName = "read_file"

// ToolDescription is the description the model sees for the tool.
const ToolDescription = "Read one file and return its text content. The path is relative to the tool's configured root directory; paths outside it are rejected. Text-like files only."

// DefaultMaxBytes bounds a file body when Config.MaxBytes is zero.
const DefaultMaxBytes int64 = 1 << 20 // 1 MiB

// schema describes the tool's single path argument; it is public contract.
var schema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"path": {
			"type": "string",
			"description": "File path relative to the root directory, without .. segments."
		}
	},
	"required": ["path"],
	"additionalProperties": false
}`)

// Config configures the read tool. Root is required; MaxBytes is the
// only optional field.
type Config struct {
	// Root is the directory every read is confined to. It must exist
	// and be a directory; symlinks in it are resolved at construction.
	Root string
	// MaxBytes caps bytes read from a file; a longer file is truncated,
	// not failed, and the result ends with a truncation marker. Zero
	// selects DefaultMaxBytes; negative values fail New.
	MaxBytes int64
}

// UnsupportedContentError reports a file whose sniffed content type is
// not text-like, so binary content never reaches the model.
type UnsupportedContentError struct {
	// Path is the requested path, relative to the root.
	Path string
	// ContentType is the sniffed media type, lowercased, without
	// parameters.
	ContentType string
}

func (e *UnsupportedContentError) Error() string {
	return fmt.Sprintf("fileread: %s: unsupported content type %q", e.Path, e.ContentType)
}

// reader holds the resolved configuration behind the tool's exec.
type reader struct {
	root     string
	maxBytes int64
}

// New validates cfg and returns the read_file tool ready for
// registration with an agent. Deps is the agent's dependency type; the
// read itself does not use it, but the tool carries it so one
// constructor serves every agent.
func New[Deps any](cfg Config) (tool.Tool[Deps], error) {
	if cfg.MaxBytes < 0 {
		return tool.Tool[Deps]{}, fmt.Errorf("fileread: max bytes must not be negative, got %d", cfg.MaxBytes)
	}
	if cfg.Root == "" {
		return tool.Tool[Deps]{}, fmt.Errorf("fileread: root is required")
	}
	root, err := filepath.EvalSymlinks(cfg.Root)
	if err != nil {
		return tool.Tool[Deps]{}, fmt.Errorf("fileread: root %s: %w", cfg.Root, err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return tool.Tool[Deps]{}, fmt.Errorf("fileread: root %s: %w", cfg.Root, err)
	}
	if !info.IsDir() {
		return tool.Tool[Deps]{}, fmt.Errorf("fileread: root %s is not a directory", cfg.Root)
	}
	maxBytes := cfg.MaxBytes
	if maxBytes == 0 {
		maxBytes = DefaultMaxBytes
	}
	r := &reader{root: root, maxBytes: maxBytes}
	return tool.New(tool.Tool[Deps]{
		Name:        ToolName,
		Description: ToolDescription,
		Schema:      schema,
		Exec: func(ctx context.Context, deps Deps, args json.RawMessage) (string, error) {
			return r.read(ctx, args)
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

// read runs one read_file call: validate the model-produced path as
// correctable arguments, resolve it inside the root, and return the
// file's text.
func (r *reader) read(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	path, err := parseArgs(args)
	if err != nil {
		return "", err
	}

	target, err := r.resolve(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return "", &model.ModelRetry{Err: fmt.Errorf("no such file: %s", path)}
		}
		return "", fmt.Errorf("fileread: %s: %w", path, err)
	}
	if info.IsDir() {
		return "", &model.ModelRetry{Err: fmt.Errorf("%s is a directory, not a file", path)}
	}
	if !info.Mode().IsRegular() {
		return "", &model.ModelRetry{Err: fmt.Errorf("%s is not a regular file", path)}
	}

	file, err := os.Open(target)
	if err != nil {
		return "", fmt.Errorf("fileread: %s: %w", path, err)
	}
	defer file.Close()
	body, truncated, err := readCapped(file, r.maxBytes)
	if err != nil {
		return "", fmt.Errorf("fileread: %s: %w", path, err)
	}

	contentType := mediaTypeOf(http.DetectContentType([]byte(body)))
	if !isTextMediaType(contentType) {
		return "", &UnsupportedContentError{Path: path, ContentType: contentType}
	}
	if truncated {
		body += fmt.Sprintf("\n\n[fileread: file truncated at %d bytes]", r.maxBytes)
	}
	return body, nil
}

// parseArgs extracts the path argument. Malformed arguments are model
// mistakes, so they reject as correctable *model.ModelRetry.
func parseArgs(args json.RawMessage) (string, error) {
	var input struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return "", &model.ModelRetry{Err: fmt.Errorf("arguments must be an object with a string path: %w", err)}
	}
	if input.Path == "" {
		return "", &model.ModelRetry{Err: fmt.Errorf("path is required")}
	}
	if filepath.IsAbs(input.Path) {
		return "", &model.ModelRetry{Err: fmt.Errorf("path must be relative to the root directory, got %q", input.Path)}
	}
	for _, segment := range strings.Split(input.Path, "/") {
		if segment == ".." {
			return "", &model.ModelRetry{Err: fmt.Errorf("path must stay within the root directory, got %q", input.Path)}
		}
	}
	return input.Path, nil
}

// resolve maps a relative path onto the root and returns the fully
// resolved target. Symlinks are resolved so a link inside the root
// cannot read a file outside it; an escaping target rejects as a
// correctable model mistake.
func (r *reader) resolve(path string) (string, error) {
	target := filepath.Join(r.root, filepath.FromSlash(path))
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		if os.IsNotExist(err) {
			return "", &model.ModelRetry{Err: fmt.Errorf("no such file: %s", path)}
		}
		return "", fmt.Errorf("fileread: %s: %w", path, err)
	}
	if !withinDir(r.root, resolved) {
		return "", &model.ModelRetry{Err: fmt.Errorf("path must stay within the root directory: %s resolves outside it", path)}
	}
	return resolved, nil
}

// withinDir reports whether path is inside dir (or equals it).
func withinDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// readCapped reads at most limit bytes and reports whether the file was
// longer. One byte past the limit is read to detect truncation without
// reading unbounded content.
func readCapped(file io.Reader, limit int64) (string, bool, error) {
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return "", false, err
	}
	truncated := int64(len(raw)) > limit
	if truncated {
		raw = raw[:limit]
	}
	return string(raw), truncated, nil
}

// passthroughMediaTypes are the sniffed types whose bodies are returned
// unchanged. Any text/* type passes through too.
var passthroughMediaTypes = map[string]bool{
	"application/json":       true,
	"application/xml":        true,
	"application/yaml":       true,
	"application/x-yaml":     true,
	"application/xhtml+xml":  true,
	"application/javascript": true,
	"text/javascript":        true,
}

func isTextMediaType(mediaType string) bool {
	return strings.HasPrefix(mediaType, "text/") || passthroughMediaTypes[mediaType]
}

// mediaTypeOf extracts the lowercase media type from a sniffed or
// header-provided value, dropping any parameters.
func mediaTypeOf(contentType string) string {
	return strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
}
