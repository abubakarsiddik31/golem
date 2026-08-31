// Package skills provides a common tool that loads Agent Skills from
// standard skill directories and returns a chosen skill's instructions
// to the model. Skills follow the open Agent Skills format
// (https://agentskills.io): a directory containing a SKILL.md file with
// YAML frontmatter (name, description) and a Markdown body.
//
// The tool implements progressive disclosure. Discovery happens once at
// construction: every <dir>/<name>/SKILL.md in the configured
// directories is parsed and validated, and only each skill's name and
// description enter the model's context as part of the tool
// description. When a task matches a description, the model calls the
// tool with that name and receives the full instructions plus the
// skill's base directory and supporting files.
//
// Skill directories are configured by the application — the package
// never reads the working directory, home directory, or environment —
// so the same tool serves a server today and a desktop bundle later.
//
// The tool is an ordinary tool.Tool[Deps]: it composes with any agent
// dependency type and every agent option. Its name, argument schema,
// and description shape (including the skill catalog) are public
// contract — models and prompts depend on them staying stable.
package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

// ToolName is the name of the tool New returns.
const ToolName = "skill"

// ToolDescription opens the description the model sees for the tool;
// New appends the discovered skill catalog to it.
const ToolDescription = "Load one available skill and return its full instructions. Call this when the current task matches a skill listed in available_skills; then follow the returned instructions."

// DefaultMaxBodyBytes bounds a skill body when Config.MaxBodyBytes is
// zero.
const DefaultMaxBodyBytes int64 = 1 << 20 // 1 MiB

// maxListedFiles caps the supporting-file list returned with a loaded
// skill.
const maxListedFiles = 20

// MaxNameLength and friends are the Agent Skills format limits enforced
// at discovery.
const (
	MaxNameLength          = 64
	MaxDescriptionLength   = 1024
	MaxCompatibilityLength = 500
)

// schema describes the tool's single name argument; it is public
// contract.
var schema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"name": {
			"type": "string",
			"description": "Skill name from the available_skills list in this tool's description."
		}
	},
	"required": ["name"],
	"additionalProperties": false
}`)

// Config configures the skill tool. Dirs is required and must contain
// at least one discoverable skill.
type Config struct {
	// Dirs are the directories scanned for <name>/SKILL.md, in
	// precedence order: when two directories hold a skill of the same
	// name, the first wins. Each directory must exist and be a
	// directory. The application resolves the paths — server config
	// today, an OS app directory or bundled path later.
	Dirs []string
	// MaxBodyBytes caps a skill body; a longer body is truncated, not
	// failed, and ends with a truncation marker. Zero selects
	// DefaultMaxBodyBytes; negative values fail New.
	MaxBodyBytes int64
}

// Skill is one discovered and validated skill. Body is the Markdown
// after the frontmatter; Dir is the resolved skill directory that
// relative references in the body resolve against.
type Skill struct {
	Name          string
	Description   string
	Compatibility string
	Body          string
	Dir           string
}

// InvalidSkillError reports a discovered SKILL.md that failed to parse
// or validate. Discovery is strict: New fails rather than silently
// serving a broken catalog.
type InvalidSkillError struct {
	// Path is the SKILL.md file that failed.
	Path string
	// Err is the parse or validation failure.
	Err error
}

func (e *InvalidSkillError) Error() string {
	return fmt.Sprintf("skills: %s: %v", e.Path, e.Err)
}

func (e *InvalidSkillError) Unwrap() error { return e.Err }

// loader holds the discovered catalog behind the tool's exec.
type loader struct {
	byName map[string]Skill
}

// Discover scans dirs for <name>/SKILL.md files, parses and validates
// each against the Agent Skills format limits, and returns the
// catalog in precedence order: the first directory containing a skill
// of a given name wins. Every configured directory must exist; every
// found SKILL.md must be valid, otherwise Discover returns an
// *InvalidSkillError naming the file.
func Discover(dirs []string) ([]Skill, error) {
	var found []Skill
	seen := make(map[string]bool)
	for _, dir := range dirs {
		root, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return nil, fmt.Errorf("skills: dir %s: %w", dir, err)
		}
		info, err := os.Stat(root)
		if err != nil {
			return nil, fmt.Errorf("skills: dir %s: %w", dir, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("skills: dir %s is not a directory", dir)
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, fmt.Errorf("skills: dir %s: %w", dir, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(root, entry.Name(), "SKILL.md")
			content, err := os.ReadFile(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, &InvalidSkillError{Path: path, Err: err}
			}
			skill, err := parseSkill(path, content)
			if err != nil {
				return nil, err
			}
			if seen[skill.Name] {
				continue
			}
			seen[skill.Name] = true
			found = append(found, skill)
		}
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("skills: no valid skills found in %d configured dir(s)", len(dirs))
	}
	return found, nil
}

// parseSkill parses and validates one SKILL.md. name must match the
// containing directory per the Agent Skills format.
func parseSkill(path string, content []byte) (Skill, error) {
	fm, body, err := parseFrontmatter(string(content))
	if err != nil {
		return Skill{}, &InvalidSkillError{Path: path, Err: err}
	}
	skill := Skill{
		Name:          fm.name,
		Description:   fm.description,
		Compatibility: fm.compatibility,
		Body:          body,
		Dir:           filepath.Dir(path),
	}
	if err := validateSkill(skill, filepath.Base(filepath.Dir(path))); err != nil {
		return Skill{}, &InvalidSkillError{Path: path, Err: err}
	}
	return skill, nil
}

// validateSkill enforces the Agent Skills format limits.
func validateSkill(skill Skill, dirName string) error {
	var errs []error
	if skill.Name == "" {
		errs = append(errs, fmt.Errorf("name is required"))
	} else {
		if len(skill.Name) > MaxNameLength {
			errs = append(errs, fmt.Errorf("name exceeds %d characters", MaxNameLength))
		}
		if !validName(skill.Name) {
			errs = append(errs, fmt.Errorf("name must be lowercase alphanumeric with single hyphen separators"))
		}
		if skill.Name != dirName {
			errs = append(errs, fmt.Errorf("name %q must match directory %q", skill.Name, dirName))
		}
	}
	if skill.Description == "" {
		errs = append(errs, fmt.Errorf("description is required"))
	} else if len(skill.Description) > MaxDescriptionLength {
		errs = append(errs, fmt.Errorf("description exceeds %d characters", MaxDescriptionLength))
	}
	if len(skill.Compatibility) > MaxCompatibilityLength {
		errs = append(errs, fmt.Errorf("compatibility exceeds %d characters", MaxCompatibilityLength))
	}
	return errors.Join(errs...)
}

// validName reports whether name matches ^[a-z0-9]+(-[a-z0-9]+)*$.
func validName(name string) bool {
	segment := false
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z' || c >= '0' && c <= '9':
			segment = true
		case c == '-':
			if !segment {
				return false
			}
			segment = false
		default:
			return false
		}
	}
	return segment
}

// New validates cfg, discovers and validates the skill catalog, and
// returns the skill tool ready for registration with an agent. The
// tool's description embeds the catalog: each skill's name and
// description, nothing more. Deps is the agent's dependency type; the
// tool itself does not use it, but the tool carries it so one
// constructor serves every agent.
func New[Deps any](cfg Config) (tool.Tool[Deps], error) {
	if len(cfg.Dirs) == 0 {
		return tool.Tool[Deps]{}, fmt.Errorf("skills: at least one directory is required")
	}
	if cfg.MaxBodyBytes < 0 {
		return tool.Tool[Deps]{}, fmt.Errorf("skills: max body bytes must not be negative, got %d", cfg.MaxBodyBytes)
	}
	maxBody := cfg.MaxBodyBytes
	if maxBody == 0 {
		maxBody = DefaultMaxBodyBytes
	}
	found, err := Discover(cfg.Dirs)
	if err != nil {
		return tool.Tool[Deps]{}, err
	}
	byName := make(map[string]Skill, len(found))
	for _, skill := range found {
		skill.Body = truncateBody(skill.Body, maxBody)
		byName[skill.Name] = skill
	}
	l := &loader{byName: byName}
	return tool.New(tool.Tool[Deps]{
		Name:        ToolName,
		Description: catalogDescription(found),
		Schema:      schema,
		Exec: func(ctx context.Context, deps Deps, args json.RawMessage) (string, error) {
			return l.load(ctx, args)
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

// catalogDescription builds the tool description: the stable preamble
// followed by the available_skills catalog.
func catalogDescription(found []Skill) string {
	var b strings.Builder
	b.WriteString(ToolDescription)
	b.WriteString("\n\n<available_skills>\n")
	for _, skill := range found {
		b.WriteString("  <skill>\n")
		fmt.Fprintf(&b, "    <name>%s</name>\n", xmlEscape(skill.Name))
		fmt.Fprintf(&b, "    <description>%s</description>\n", xmlEscape(skill.Description))
		b.WriteString("  </skill>\n")
	}
	b.WriteString("</available_skills>")
	return b.String()
}

// load runs one skill call: validate the model-produced name as
// correctable arguments, then return the skill's instructions with its
// base directory and supporting files.
func (l *loader) load(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var input struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return "", &model.ModelRetry{Err: fmt.Errorf("arguments must be an object with a string name: %w", err)}
	}
	if input.Name == "" {
		return "", &model.ModelRetry{Err: fmt.Errorf("name is required")}
	}
	skill, ok := l.byName[input.Name]
	if !ok {
		return "", &model.ModelRetry{Err: fmt.Errorf("unknown skill %q; available skills: %s", input.Name, strings.Join(l.names(), ", "))}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "<skill_content name=%q>\n", skill.Name)
	fmt.Fprintf(&b, "# Skill: %s\n\n", skill.Name)
	b.WriteString(skill.Body)
	if !strings.HasSuffix(skill.Body, "\n") {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\nBase directory for this skill: %s\n", skill.Dir)
	b.WriteString("Relative paths in this skill (e.g. scripts/, references/, assets/) are relative to this base directory.\n")
	files, err := supportingFiles(skill.Dir)
	if err != nil {
		return "", fmt.Errorf("skills: %s: %w", skill.Name, err)
	}
	if len(files) > 0 {
		b.WriteString("\n<skill_files>\n")
		for _, file := range files {
			fmt.Fprintf(&b, "<file>%s</file>\n", file)
		}
		b.WriteString("</skill_files>\n")
		if len(files) == maxListedFiles {
			b.WriteString("[skills: file list truncated]\n")
		}
	}
	b.WriteString("</skill_content>")
	return b.String(), nil
}

// names returns the catalog names in discovery order.
func (l *loader) names() []string {
	out := make([]string, 0, len(l.byName))
	for name := range l.byName {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// supportingFiles lists files in the skill directory besides SKILL.md,
// as slash-relative paths, sorted, capped at maxListedFiles.
func supportingFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if rel == "SKILL.md" {
			return nil
		}
		if len(files) == maxListedFiles {
			return filepath.SkipAll
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// truncateBody caps body at limit bytes, appending a truncation marker
// like fileread does.
func truncateBody(body string, limit int64) string {
	if int64(len(body)) <= limit {
		return body
	}
	return body[:limit] + fmt.Sprintf("\n\n[skills: body truncated at %d bytes]", limit)
}

// xmlEscape escapes text for inclusion in the XML catalog.
func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}
