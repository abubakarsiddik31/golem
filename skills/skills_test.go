package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
)

// nameArgs builds the model-produced arguments for a load.
func nameArgs(name string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"name": %q}`, name))
}

// writeSkill creates one valid skill directory and returns its root.
func writeSkill(t *testing.T, root, name, description, body string, extraFiles map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n%s", name, description, body)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	for rel, data := range extraFiles {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestNewValidatesConfig(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "plain.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"no dirs", Config{}, "at least one directory is required"},
		{"negative max body", Config{Dirs: []string{root}, MaxBodyBytes: -1}, "max body bytes must not be negative"},
		{"missing dir", Config{Dirs: []string{filepath.Join(root, "nope")}}, "nope"},
		{"dir is a file", Config{Dirs: []string{file}}, "is not a directory"},
		{"empty dir", Config{Dirs: []string{root}}, "no valid skills found"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := New[struct{}](testCase.cfg); err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("New(%+v) error = %v, want containing %q", testCase.cfg, err, testCase.want)
			}
		})
	}
}

func TestNewRejectsInvalidSkillFiles(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{
			"missing description",
			"---\nname: git-release\n---\nbody",
			"description is required",
		},
		{
			"name does not match directory",
			"---\nname: other-name\ndescription: d\n---\nbody",
			`name "other-name" must match directory "git-release"`,
		},
		{
			"uppercase name",
			"---\nname: Git-Release\ndescription: d\n---\nbody",
			"lowercase alphanumeric",
		},
		{
			"consecutive hyphens",
			"---\nname: git--release\ndescription: d\n---\nbody",
			"lowercase alphanumeric",
		},
		{
			"no frontmatter",
			"just markdown",
			"frontmatter",
		},
		{
			"oversize description",
			fmt.Sprintf("---\nname: git-release\ndescription: %s\n---\nbody", strings.Repeat("d", MaxDescriptionLength+1)),
			"description exceeds 1024 characters",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := writeSkill(t, t.TempDir(), "git-release", "placeholder", "", nil)
			if err := os.WriteFile(filepath.Join(root, "git-release", "SKILL.md"), []byte(testCase.content), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := New[struct{}](Config{Dirs: []string{root}})
			var invalid *InvalidSkillError
			if !errors.As(err, &invalid) {
				t.Fatalf("New error = %v, want *InvalidSkillError in the chain", err)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("error = %v, want containing %q", err, testCase.want)
			}
		})
	}
}

func TestCatalogInToolDescription(t *testing.T) {
	root := writeSkill(t, t.TempDir(), "git-release", "Create consistent releases", "body", nil)
	loading := MustNew[struct{}](Config{Dirs: []string{root}})

	if loading.Name != ToolName {
		t.Errorf("Name = %q, want %q", loading.Name, ToolName)
	}
	if !strings.HasPrefix(loading.Description, ToolDescription) {
		t.Errorf("Description does not start with the exported preamble: %q", loading.Description)
	}
	for _, want := range []string{
		"<available_skills>",
		"<name>git-release</name>",
		"<description>Create consistent releases</description>",
		"</available_skills>",
	} {
		if !strings.Contains(loading.Description, want) {
			t.Errorf("Description missing %q:\n%s", want, loading.Description)
		}
	}
	if !json.Valid(loading.Schema) {
		t.Errorf("Schema is not valid JSON: %s", loading.Schema)
	}
}

func TestCatalogEscapesXMLText(t *testing.T) {
	root := writeSkill(t, t.TempDir(), "tagged", "Uses <skill> & \"quotes\"", "body", nil)
	loading := MustNew[struct{}](Config{Dirs: []string{root}})
	// The raw user text must be escaped so it cannot forge markup.
	if strings.Contains(loading.Description, "Uses <skill>") {
		t.Errorf("Description contains unescaped markup:\n%s", loading.Description)
	}
	if !strings.Contains(loading.Description, "Uses &lt;skill&gt; &amp; &quot;quotes&quot;") {
		t.Errorf("Description missing escaped text:\n%s", loading.Description)
	}
}

func TestFirstDirWinsOnDuplicateNames(t *testing.T) {
	first := writeSkill(t, t.TempDir(), "shared", "first description", "first body", nil)
	second := writeSkill(t, t.TempDir(), "shared", "second description", "second body", nil)

	loading := MustNew[struct{}](Config{Dirs: []string{first, second}})
	if !strings.Contains(loading.Description, "first description") || strings.Contains(loading.Description, "second description") {
		t.Errorf("catalog should keep the first dir's skill, got:\n%s", loading.Description)
	}
	result, err := loading.Exec(context.Background(), struct{}{}, nameArgs("shared"))
	if err != nil {
		t.Fatalf("Exec error = %v", err)
	}
	if !strings.Contains(result, "first body") {
		t.Errorf("Exec result = %q, want the first dir's body", result)
	}
}

func TestLoadReturnsBodyBaseDirAndFiles(t *testing.T) {
	root := writeSkill(t, t.TempDir(), "git-release", "Create releases",
		"Draft notes, then run scripts/release.sh.\n", map[string]string{
			"scripts/release.sh":  "gh release create\n",
			"references/links.md": "# Links\n",
		})
	loading := MustNew[struct{}](Config{Dirs: []string{root}})

	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := loading.Exec(context.Background(), struct{}{}, nameArgs("git-release"))
	if err != nil {
		t.Fatalf("Exec error = %v", err)
	}
	for _, want := range []string{
		`<skill_content name="git-release">`,
		"# Skill: git-release",
		"Draft notes, then run scripts/release.sh.",
		fmt.Sprintf("Base directory for this skill: %s", filepath.Join(resolved, "git-release")),
		"<skill_files>",
		"<file>references/links.md</file>",
		"<file>scripts/release.sh</file>",
		"</skill_files>",
		"</skill_content>",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("Exec result missing %q:\n%s", want, result)
		}
	}
}

func TestLoadWithoutSupportingFilesOmitsFileList(t *testing.T) {
	root := writeSkill(t, t.TempDir(), "lonely", "No extra files", "body", nil)
	loading := MustNew[struct{}](Config{Dirs: []string{root}})
	result, err := loading.Exec(context.Background(), struct{}{}, nameArgs("lonely"))
	if err != nil {
		t.Fatalf("Exec error = %v", err)
	}
	if strings.Contains(result, "<skill_files>") {
		t.Errorf("result should omit skill_files when the skill has no extra files:\n%s", result)
	}
}

func TestUnknownNameIsCorrectable(t *testing.T) {
	root := writeSkill(t, t.TempDir(), "known", "Known skill", "body", nil)
	loading := MustNew[struct{}](Config{Dirs: []string{root}})
	_, err := loading.Exec(context.Background(), struct{}{}, nameArgs("unknown"))
	var retry *model.ModelRetry
	if !errors.As(err, &retry) {
		t.Fatalf("Exec error = %v, want *model.ModelRetry in the chain", err)
	}
	if !strings.Contains(retry.Err.Error(), "known") {
		t.Errorf("retry error %q should list the available skill names", retry.Err)
	}
}

func TestBadArgumentsAreCorrectable(t *testing.T) {
	root := writeSkill(t, t.TempDir(), "known", "Known skill", "body", nil)
	loading := MustNew[struct{}](Config{Dirs: []string{root}})
	cases := []struct {
		name string
		args json.RawMessage
	}{
		{"not json", json.RawMessage(`{`)},
		{"not an object", json.RawMessage(`"known"`)},
		{"missing name", json.RawMessage(`{}`)},
		{"empty name", json.RawMessage(`{"name":""}`)},
		{"non-string name", json.RawMessage(`{"name":42}`)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := loading.Exec(context.Background(), struct{}{}, testCase.args)
			var retry *model.ModelRetry
			if !errors.As(err, &retry) {
				t.Fatalf("Exec(%s) error = %v, want *model.ModelRetry in the chain", testCase.args, err)
			}
		})
	}
}

func TestTruncatesLargeBodies(t *testing.T) {
	root := writeSkill(t, t.TempDir(), "long", "Long body", strings.Repeat("a", 100), nil)
	loading := MustNew[struct{}](Config{Dirs: []string{root}, MaxBodyBytes: 16})
	result, err := loading.Exec(context.Background(), struct{}{}, nameArgs("long"))
	if err != nil {
		t.Fatalf("Exec error = %v", err)
	}
	if !strings.Contains(result, strings.Repeat("a", 16)+"\n\n[skills: body truncated at 16 bytes]") {
		t.Errorf("Exec result missing truncated body:\n%s", result)
	}
}

func TestHonorsCanceledContext(t *testing.T) {
	root := writeSkill(t, t.TempDir(), "known", "Known skill", "body", nil)
	loading := MustNew[struct{}](Config{Dirs: []string{root}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := loading.Exec(ctx, struct{}{}, nameArgs("known"))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Exec error = %v, want context.Canceled", err)
	}
}

func TestComposesWithAgent(t *testing.T) {
	root := writeSkill(t, t.TempDir(), "git-release", "Create releases", "Draft the notes.\n", nil)
	loading := MustNew[struct{}](Config{Dirs: []string{root}})
	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "call-1", Name: ToolName, Args: nameArgs("git-release")},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "release drafted"}},
	)
	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](loading),
	)
	if err != nil {
		t.Fatalf("golem.New error = %v", err)
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "prepare the release")
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if result.Output != "release drafted" {
		t.Errorf("Output = %q, want %q", result.Output, "release drafted")
	}
	var toolEvidence string
	for _, message := range result.Messages {
		if message.Role == model.RoleTool {
			toolEvidence = message.Content
		}
	}
	if !strings.Contains(toolEvidence, "Draft the notes.") {
		t.Errorf("tool result message = %q, want the skill body", toolEvidence)
	}
}

func TestFrontmatterParsing(t *testing.T) {
	cases := []struct {
		name        string
		content     string
		wantName    string
		wantDesc    string
		wantCompat  string
		wantBody    string
		description string
	}{
		{
			"plain scalars",
			"---\nname: a\ndescription: does things\ncompatibility: golem\n---\nBody here.\n",
			"a", "does things", "golem", "Body here.\n", "",
		},
		{
			"quoted scalars",
			"---\nname: \"a\"\ndescription: 'Uses ''quotes'' here'\n---\nBody\n",
			"a", "Uses 'quotes' here", "", "Body\n", "",
		},
		{
			"folded block description",
			"---\nname: a\ndescription: >-\n  Line one\n  continues here.\n---\nBody\n",
			"a", "Line one continues here.", "", "Body\n", "",
		},
		{
			"literal block description",
			"---\nname: a\ndescription: |\n  Line one\n  Line two\n---\nBody\n",
			"a", "Line one\nLine two", "", "Body\n", "",
		},
		{
			"ignored fields and nested metadata",
			"---\nname: a\ndescription: d\nlicense: MIT\nmetadata:\n  audience: all\n  note: with: colon\n---\nBody\n",
			"a", "d", "", "Body\n", "",
		},
		{
			"description before name",
			"---\ndescription: first field\nname: a\n---\nBody\n",
			"a", "first field", "", "Body\n", "",
		},
		{
			"windows line endings",
			"---\r\nname: a\r\ndescription: d\r\n---\r\nBody\r\n",
			"a", "d", "", "Body\r\n", "",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fm, body, err := parseFrontmatter(testCase.content)
			if err != nil {
				t.Fatalf("parseFrontmatter error = %v", err)
			}
			if fm.name != testCase.wantName {
				t.Errorf("name = %q, want %q", fm.name, testCase.wantName)
			}
			if fm.description != testCase.wantDesc {
				t.Errorf("description = %q, want %q", fm.description, testCase.wantDesc)
			}
			if fm.compatibility != testCase.wantCompat {
				t.Errorf("compatibility = %q, want %q", fm.compatibility, testCase.wantCompat)
			}
			if body != testCase.wantBody {
				t.Errorf("body = %q, want %q", body, testCase.wantBody)
			}
		})
	}
}

func TestParseFrontmatterRejectsMissingBlock(t *testing.T) {
	for _, content := range []string{"", "no frontmatter", "---\nname: a\n"} {
		if _, _, err := parseFrontmatter(content); err == nil {
			t.Errorf("parseFrontmatter(%q) error = nil, want an error", content)
		}
	}
}
