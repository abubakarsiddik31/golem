package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// FileStore persists notebook files as Markdown files under a root
// directory, one directory per namespace. Writes are atomic within the
// process and versioned with a SHA-256 content hash; the store is safe
// for concurrent use, but concurrent processes sharing one directory can
// still race (compare-and-swap is enforced in-process only).
//
// The store never follows a symlink that escapes the root, and List skips
// symlinked files and directories. There is no metadata sidecar: write
// metadata is a backend feature this store does not support.
type FileStore struct {
	root string
	mu   sync.Mutex
}

var (
	_ Store          = (*FileStore)(nil)
	_ VersionedStore = (*FileStore)(nil)
)

// NewFileStore creates or opens root and returns a file-backed store.
func NewFileStore(root string) (*FileStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("memory: file store root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("memory: file store root: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("memory: create file store root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("memory: resolve file store root: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("memory: stat file store root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("memory: file store root %s is not a directory", resolved)
	}
	return &FileStore{root: resolved}, nil
}

// List returns regular files under the namespace, sorted, skipping
// symlinks.
func (f *FileStore) List(_ context.Context, namespace string) ([]string, error) {
	if err := validateNamespace(namespace); err != nil {
		return nil, err
	}
	dir := filepath.Join(f.root, filepath.FromSlash(namespace))
	info, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("memory: list %s: %w", namespace, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("memory: namespace %q is not a directory", namespace)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, fmt.Errorf("memory: list %s: %w", namespace, err)
	}
	if !f.inside(real) {
		return nil, fmt.Errorf("memory: namespace %q resolves outside the store", namespace)
	}
	var paths []string
	err = filepath.WalkDir(real, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(real, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("memory: list %s: %w", namespace, err)
	}
	sort.Strings(paths)
	return paths, nil
}

// Read returns one file and its content version.
func (f *FileStore) Read(_ context.Context, namespace, path string) (File, error) {
	target, err := f.safeTarget(namespace, path)
	if err != nil {
		return File{}, err
	}
	real, err := filepath.EvalSymlinks(target)
	if errors.Is(err, os.ErrNotExist) {
		return File{}, ErrNotFound
	}
	if err != nil {
		return File{}, fmt.Errorf("memory: read %s: %w", path, err)
	}
	if !f.inside(real) {
		return File{}, fmt.Errorf("memory: %s resolves outside the store", path)
	}
	info, err := os.Stat(real)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return File{}, ErrNotFound
		}
		return File{}, fmt.Errorf("memory: read %s: %w", path, err)
	}
	if info.IsDir() {
		return File{}, fmt.Errorf("memory: %s is a directory", path)
	}
	content, err := os.ReadFile(real)
	if err != nil {
		return File{}, fmt.Errorf("memory: read %s: %w", path, err)
	}
	return File{Path: path, Content: string(content), Version: fileVersion(content)}, nil
}

// Write stores or replaces a file without a version check.
func (f *FileStore) Write(ctx context.Context, namespace, path, content string) error {
	return f.WriteVersioned(ctx, namespace, path, content, "\x00overwrite")
}

// WriteVersioned stores content when expectedVersion matches the current
// file. An empty expectedVersion means create-only.
func (f *FileStore) WriteVersioned(_ context.Context, namespace, path, content, expectedVersion string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	target, err := f.safeTarget(namespace, path)
	if err != nil {
		return err
	}
	if expectedVersion != "\x00overwrite" {
		current, err := f.readVersion(target)
		switch {
		case errors.Is(err, ErrNotFound):
			if expectedVersion != "" {
				return ErrVersionConflict
			}
		case err != nil:
			return err
		case expectedVersion == "", current != expectedVersion:
			return ErrVersionConflict
		}
	}
	return f.atomicWrite(target, content)
}

// Delete removes a file without a version check.
func (f *FileStore) Delete(_ context.Context, namespace, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	target, err := f.safeTarget(namespace, path)
	if err != nil {
		return err
	}
	if err := os.Remove(target); errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("memory: delete %s: %w", path, err)
	}
	return nil
}

// DeleteVersioned removes a file when expectedVersion matches.
func (f *FileStore) DeleteVersioned(_ context.Context, namespace, path, expectedVersion string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	target, err := f.safeTarget(namespace, path)
	if err != nil {
		return err
	}
	current, err := f.readVersion(target)
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if current != expectedVersion {
		return ErrVersionConflict
	}
	if err := os.Remove(target); errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("memory: delete %s: %w", path, err)
	}
	return nil
}

func (f *FileStore) safeTarget(namespace, path string) (string, error) {
	if err := validateNamespace(namespace); err != nil {
		return "", err
	}
	if err := validatePath(path); err != nil {
		return "", err
	}
	target := filepath.Join(f.root, filepath.FromSlash(namespace), filepath.FromSlash(path))
	relative, err := filepath.Rel(f.root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("memory: path %q escapes the store", path)
	}
	return target, nil
}

func (f *FileStore) inside(path string) bool {
	relative, err := filepath.Rel(f.root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (f *FileStore) readVersion(target string) (string, error) {
	real, err := filepath.EvalSymlinks(target)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("memory: read version: %w", err)
	}
	if !f.inside(real) {
		return "", fmt.Errorf("memory: file resolves outside the store")
	}
	content, err := os.ReadFile(real)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("memory: read version: %w", err)
	}
	return fileVersion(content), nil
}

func (f *FileStore) atomicWrite(target, content string) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("memory: create directory: %w", err)
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fmt.Errorf("memory: resolve directory: %w", err)
	}
	if !f.inside(realDir) {
		return fmt.Errorf("memory: directory resolves outside the store")
	}
	temp, err := os.CreateTemp(realDir, ".memory-*")
	if err != nil {
		return fmt.Errorf("memory: create temp file: %w", err)
	}
	name := temp.Name()
	cleanup := func() {
		temp.Close()
		os.Remove(name)
	}
	if _, err := temp.WriteString(content); err != nil {
		cleanup()
		return fmt.Errorf("memory: write temp file: %w", err)
	}
	if err := temp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("memory: close temp file: %w", err)
	}
	if err := os.Chmod(name, 0o600); err != nil {
		cleanup()
		return fmt.Errorf("memory: chmod temp file: %w", err)
	}
	if err := os.Rename(name, target); err != nil {
		cleanup()
		return fmt.Errorf("memory: replace file: %w", err)
	}
	return nil
}

func fileVersion(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
