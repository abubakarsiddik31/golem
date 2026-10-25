package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

type searchDeps struct{}

// searchTestTool builds a declared tool with a schema listing properties.
func searchTestTool(t *testing.T, name, description string, properties ...string) tool.Tool[searchDeps] {
	t.Helper()
	schema := map[string]any{"type": "object"}
	if len(properties) > 0 {
		props := make(map[string]any, len(properties))
		for _, property := range properties {
			props[property] = map[string]any{"type": "string"}
		}
		schema["properties"] = props
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal schema for %s: %v", name, err)
	}
	return tool.MustNew(tool.Tool[searchDeps]{
		Name:        name,
		Description: description,
		Schema:      raw,
		Exec: func(context.Context, searchDeps, json.RawMessage) (tool.Result, error) {
			return tool.Text("ok"), nil
		},
	})
}

// searchIndexFor defers every tool it is given.
func searchIndexFor(t *testing.T, limit int, tools ...tool.Tool[searchDeps]) *searchIndex {
	t.Helper()
	names := make([]string, len(tools))
	for i, declared := range tools {
		names[i] = declared.Name
	}
	index := newSearchIndex(&SearchConfig{Deferred: names, Limit: limit}, tools)
	if index == nil {
		t.Fatal("newSearchIndex returned nil for a non-empty catalog")
	}
	return index
}

func entryNames(entries []searchEntry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.name
	}
	return names
}

func specNames(specs []model.ToolSpec) []string {
	names := make([]string, len(specs))
	for i, spec := range specs {
		names[i] = spec.Name
	}
	return names
}

func searchCall(id, query string) model.ToolCall {
	args, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		panic(err)
	}
	return model.ToolCall{ID: id, Name: SearchToolName, Args: args}
}

func TestSearchTokensSplitsOnSeparatorsAndCamelCase(t *testing.T) {
	t.Parallel()

	cases := map[string][]string{
		"mortgage_calculator": {"mortgage", "calculator"},
		"MortgageCalculator":  {"mortgage", "calculator"},
		"PDF2Text":            {"pdf2", "text"},
		"  spaced--out  ":     {"spaced", "out"},
		"":                    nil,
	}
	for input, want := range cases {
		if got := searchTokens(input); !reflect.DeepEqual(got, want) {
			t.Errorf("searchTokens(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestSearchWeighsNameOverSchemaOverDescription(t *testing.T) {
	t.Parallel()

	weather := searchTestTool(t, "weather", "Report the forecast", "location")
	stock := searchTestTool(t, "stock_price", "Look up a stock quote", "symbol")
	index := searchIndexFor(t, 0, weather, stock)

	cases := []struct {
		entry int
		query string
		want  int
	}{
		{0, "weather", 3},
		{0, "location", 2},
		{0, "forecast", 1},
		{1, "stock", 3},
		{1, "symbol", 2},
		{1, "quote", 1},
	}
	for _, tc := range cases {
		got := index.entries[tc.entry].score(uniqueTokens(searchTokens(tc.query)))
		if got != tc.want {
			t.Errorf("entry %q scored %d for %q, want %d", index.entries[tc.entry].name, got, tc.query, tc.want)
		}
	}
}

func TestSearchPrefixScoresOneLessThanExact(t *testing.T) {
	t.Parallel()

	index := searchIndexFor(t, 0, searchTestTool(t, "weather", "Report the forecast"))
	entry := index.entries[0]
	exact := entry.score([]string{"weather"})
	for _, prefix := range []string{"weath", "wea"} {
		if got := entry.score([]string{prefix}); got != exact-1 {
			t.Errorf("prefix %q scored %d, want %d", prefix, got, exact-1)
		}
	}
}

func TestSearchOrdersByScoreThenNameAndCapsAtLimit(t *testing.T) {
	t.Parallel()

	tools := []tool.Tool[searchDeps]{
		searchTestTool(t, "beta", "data", "query"),
		searchTestTool(t, "alpha", "data", "query"),
		searchTestTool(t, "gamma", "data", "query"),
	}
	index := searchIndexFor(t, 2, tools...)

	if got := entryNames(index.match("data")); !reflect.DeepEqual(got, []string{"alpha", "beta"}) {
		t.Fatalf("match = %v, want [alpha beta]", got)
	}
}

func TestSearchDefaultLimitIsFive(t *testing.T) {
	t.Parallel()

	var tools []tool.Tool[searchDeps]
	for i := 0; i < 6; i++ {
		tools = append(tools, searchTestTool(t, fmt.Sprintf("data_%d", i), "data", "query"))
	}
	index := searchIndexFor(t, 0, tools...)

	if index.limit != DefaultSearchLimit {
		t.Fatalf("limit = %d, want %d", index.limit, DefaultSearchLimit)
	}
	if got := len(index.match("data")); got != DefaultSearchLimit {
		t.Fatalf("match returned %d entries, want %d", got, DefaultSearchLimit)
	}
}

func TestRevealedDerivesFromSearchCallsInDeclarationOrder(t *testing.T) {
	t.Parallel()

	weather := searchTestTool(t, "weather", "Report the forecast")
	stock := searchTestTool(t, "stock_price", "Look up a quote")
	index := searchIndexFor(t, 0, weather, stock)

	messages := []model.Message{{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
		searchCall("s1", "stock weather"),
	}}}
	// stock_price ranks first, but reveals come back in declaration order.
	if got := index.revealed(messages); !reflect.DeepEqual(got, []string{"weather", "stock_price"}) {
		t.Fatalf("revealed = %v, want [weather stock_price]", got)
	}
}

func TestRevealedIgnoresMalformedSearchArguments(t *testing.T) {
	t.Parallel()

	index := searchIndexFor(t, 0, searchTestTool(t, "weather", "Report the forecast"))
	messages := []model.Message{{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
		{ID: "s1", Name: SearchToolName, Args: json.RawMessage(`{`)},
	}}}

	if got := index.revealed(messages); len(got) != 0 {
		t.Fatalf("revealed = %v, want none", got)
	}
}

func TestAdvertisedSpecsAddsSearchToolAndRevealedTools(t *testing.T) {
	t.Parallel()

	echo := searchTestTool(t, "echo", "Echo input")
	weather := searchTestTool(t, "weather", "Report the forecast")
	index := searchIndexFor(t, 0, weather)
	tools := []tool.Tool[searchDeps]{echo, weather}
	base := []model.ToolSpec{{Name: "echo"}}

	if got := specNames(advertisedSpecs(index, base, tools, nil)); !reflect.DeepEqual(got, []string{"echo", SearchToolName}) {
		t.Fatalf("initial specs = %v, want [echo %s]", got, SearchToolName)
	}
	messages := []model.Message{{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{searchCall("s1", "weather")}}}
	if got := specNames(advertisedSpecs(index, base, tools, messages)); !reflect.DeepEqual(got, []string{"echo", SearchToolName, "weather"}) {
		t.Fatalf("revealed specs = %v, want [echo %s weather]", got, SearchToolName)
	}
}

func TestAdvertisedSpecsWithoutSearchReturnsBase(t *testing.T) {
	t.Parallel()

	base := []model.ToolSpec{{Name: "echo"}}
	got := advertisedSpecs[searchDeps](nil, base, nil, nil)
	if !reflect.DeepEqual(specNames(got), []string{"echo"}) {
		t.Fatalf("specs = %v, want [echo]", specNames(got))
	}
}

func TestCheckAvailableRejectsUnrevealedDeferredTool(t *testing.T) {
	t.Parallel()

	index := searchIndexFor(t, 0, searchTestTool(t, "weather", "Report the forecast"))
	calls := []model.ToolCall{{ID: "w1", Name: "weather", Args: json.RawMessage(`{}`)}}

	err := index.checkAvailable(calls, nil)
	var toolErr *ToolError
	if !errors.As(err, &toolErr) {
		t.Fatalf("checkAvailable error = %v, want *ToolError", err)
	}
	if toolErr.ToolName != "weather" {
		t.Fatalf("ToolError.ToolName = %q, want weather", toolErr.ToolName)
	}
	if !strings.Contains(err.Error(), SearchToolName) {
		t.Fatalf("error %q does not name %s", err, SearchToolName)
	}

	revealed := []model.Message{{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{searchCall("s1", "weather")}}}
	if err := index.checkAvailable(calls, revealed); err != nil {
		t.Fatalf("checkAvailable after reveal = %v, want nil", err)
	}
	if err := index.checkAvailable([]model.ToolCall{{ID: "e1", Name: "echo"}}, nil); err != nil {
		t.Fatalf("checkAvailable for eager tool = %v, want nil", err)
	}
}

func TestSearchToolRejectsBadArgumentsAsModelRetry(t *testing.T) {
	t.Parallel()

	index := searchIndexFor(t, 0, searchTestTool(t, "weather", "Report the forecast"))
	declared := searchTool[searchDeps](index)

	for _, args := range []string{`{`, `{"query":""}`, `{"query":"   "}`} {
		_, err := declared.Exec(context.Background(), searchDeps{}, json.RawMessage(args))
		var retry *model.ModelRetry
		if !errors.As(err, &retry) {
			t.Errorf("Exec(%s) error = %v, want *model.ModelRetry", args, err)
		}
	}

	result, err := declared.Exec(context.Background(), searchDeps{}, json.RawMessage(`{"query":"weather"}`))
	if err != nil {
		t.Fatalf("Exec(valid query) error = %v", err)
	}
	if !strings.Contains(result.Text, "weather(") {
		t.Fatalf("result %q does not describe the matched tool", result.Text)
	}
}

func TestRenderExplainsNoMatch(t *testing.T) {
	t.Parallel()

	index := searchIndexFor(t, 0, searchTestTool(t, "weather", "Report the forecast"))
	if text := index.render("database"); !strings.Contains(text, "No tools matched") {
		t.Fatalf("render = %q, want a no-match explanation", text)
	}
}

func TestNewSearchIndexDisabledWithoutDeferredTools(t *testing.T) {
	t.Parallel()

	if index := newSearchIndex[searchDeps](nil, nil); index != nil {
		t.Fatalf("newSearchIndex(nil) = %+v, want nil", index)
	}
	if index := newSearchIndex[searchDeps](&SearchConfig{}, nil); index != nil {
		t.Fatalf("newSearchIndex(empty) = %+v, want nil", index)
	}
	if index := newSearchIndex(&SearchConfig{Deferred: []string{"missing"}},
		[]tool.Tool[searchDeps]{searchTestTool(t, "weather", "Report the forecast")}); index != nil {
		t.Fatalf("newSearchIndex(unresolved) = %+v, want nil", index)
	}
}

func TestSchemaPropertiesSorted(t *testing.T) {
	t.Parallel()

	declared := searchTestTool(t, "lookup", "Look something up", "zeta", "alpha")
	if got := schemaProperties(declared.Schema); !reflect.DeepEqual(got, []string{"alpha", "zeta"}) {
		t.Fatalf("schemaProperties = %v, want [alpha zeta]", got)
	}
	if got := schemaProperties(json.RawMessage(`not json`)); got != nil {
		t.Fatalf("schemaProperties(invalid) = %v, want nil", got)
	}
}
