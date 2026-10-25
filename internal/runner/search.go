package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/tool"
)

const (
	// SearchToolName is the name of the discovery tool the runner
	// synthesizes when tools are deferred. It is reserved for that use.
	SearchToolName = "search_tools"
	// DefaultSearchLimit caps matches per search when SearchConfig.Limit
	// leaves it unset.
	DefaultSearchLimit = 5
)

// SearchConfig hides part of a run's tool catalog behind a runner-owned
// discovery tool. Deferred tools stay declared — and dispatchable — but
// are not advertised until a search matches them.
type SearchConfig struct {
	// Deferred names the tools that start hidden.
	Deferred []string
	// Limit caps matches per search. Zero means DefaultSearchLimit.
	Limit int
}

// searchIndex is the prepared deferred catalog: one entry per deferred
// tool in declaration order, plus the name set used to reject calls to
// tools the model has not revealed.
type searchIndex struct {
	limit    int
	entries  []searchEntry
	deferred map[string]struct{}
}

// searchEntry is one deferred tool reduced to what matching and the
// search result need.
type searchEntry struct {
	name        string
	description string
	args        []string
	// weights maps a token to the strongest weight it carries across
	// the tool's name, argument names, and description.
	weights map[string]int
}

// newSearchIndex prepares the deferred catalog. It returns nil when no
// tool is deferred, or when none of the named tools resolve, which
// disables search; the agent validates that every deferred name exists.
func newSearchIndex[Deps any](config *SearchConfig, tools []tool.Tool[Deps]) *searchIndex {
	if config == nil || len(config.Deferred) == 0 {
		return nil
	}
	deferred := make(map[string]struct{}, len(config.Deferred))
	for _, name := range config.Deferred {
		deferred[name] = struct{}{}
	}
	limit := config.Limit
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	index := &searchIndex{limit: limit, deferred: deferred}
	for _, t := range tools {
		if _, ok := deferred[t.Name]; ok {
			index.entries = append(index.entries, newSearchEntry(t))
		}
	}
	if len(index.entries) == 0 {
		return nil
	}
	return index
}

// newSearchEntry tokenizes one tool's name, argument names, and
// description. Name tokens weigh most because a model that knows what it
// wants usually knows the name; description words weigh least because
// they are prose.
func newSearchEntry[Deps any](t tool.Tool[Deps]) searchEntry {
	entry := searchEntry{name: t.Name, description: t.Description, weights: make(map[string]int)}
	add := func(tokens []string, weight int) {
		for _, token := range tokens {
			if current, ok := entry.weights[token]; !ok || weight > current {
				entry.weights[token] = weight
			}
		}
	}
	add(searchTokens(t.Name), 3)
	entry.args = schemaProperties(t.Schema)
	for _, name := range entry.args {
		add(searchTokens(name), 2)
	}
	add(searchTokens(t.Description), 1)
	return entry
}

// score sums the strongest weight of each distinct query token. A token
// that only prefixes a catalog token scores one less than an exact hit,
// so "calc" still reaches "calculator" without outranking a real match.
func (e searchEntry) score(tokens []string) int {
	score := 0
	for _, token := range tokens {
		if weight, ok := e.weights[token]; ok {
			score += weight
			continue
		}
		best := 0
		for known, weight := range e.weights {
			if weight-1 > best && strings.HasPrefix(known, token) {
				best = weight - 1
			}
		}
		score += best
	}
	return score
}

// match ranks the catalog against a query, best score first and ties by
// name, and caps the result at the configured limit.
func (s *searchIndex) match(query string) []searchEntry {
	tokens := uniqueTokens(searchTokens(query))
	if len(tokens) == 0 {
		return nil
	}
	type scored struct {
		entry searchEntry
		score int
	}
	var hits []scored
	for _, entry := range s.entries {
		if score := entry.score(tokens); score > 0 {
			hits = append(hits, scored{entry: entry, score: score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].entry.name < hits[j].entry.name
	})
	if len(hits) > s.limit {
		hits = hits[:s.limit]
	}
	matches := make([]searchEntry, len(hits))
	for i, hit := range hits {
		matches[i] = hit.entry
	}
	return matches
}

// revealed derives the revealed set from the run's messages: every
// search_tools call is re-matched, so a reveal survives a correction
// round or a resumed run without any stored state. Names come back in
// declaration order, not discovery order.
func (s *searchIndex) revealed(messages []model.Message) []string {
	found := make(map[string]struct{})
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if call.Name != SearchToolName {
				continue
			}
			var args struct {
				Query string `json:"query"`
			}
			if json.Unmarshal(call.Args, &args) != nil {
				continue
			}
			for _, match := range s.match(args.Query) {
				found[match.name] = struct{}{}
			}
		}
	}
	names := make([]string, 0, len(found))
	for _, entry := range s.entries {
		if _, ok := found[entry.name]; ok {
			names = append(names, entry.name)
		}
	}
	return names
}

// advertisedSpecs returns the specs this turn may show: the base set,
// the discovery tool, and the specs of revealed tools in declaration
// order. A nil index advertises the base set unchanged.
func advertisedSpecs[Deps any](index *searchIndex, base []model.ToolSpec, tools []tool.Tool[Deps], messages []model.Message) []model.ToolSpec {
	if index == nil {
		return base
	}
	specs := make([]model.ToolSpec, 0, len(base)+1+len(index.entries))
	shown := make(map[string]struct{}, len(base)+1)
	for _, spec := range base {
		specs = append(specs, spec)
		shown[spec.Name] = struct{}{}
	}
	if _, ok := shown[SearchToolName]; !ok {
		specs = append(specs, searchToolSpec())
		shown[SearchToolName] = struct{}{}
	}
	for _, name := range index.revealed(messages) {
		if _, ok := shown[name]; ok {
			continue
		}
		for _, t := range tools {
			if t.Name != name {
				continue
			}
			specs = append(specs, model.ToolSpec{Name: t.Name, Description: t.Description, Schema: t.Schema})
			shown[name] = struct{}{}
			break
		}
	}
	return specs
}

// checkAvailable rejects calls to deferred tools the model has not
// revealed. The tool is declared, so dispatch would succeed, but the
// model cannot know its schema — the call is a guess.
func (s *searchIndex) checkAvailable(calls []model.ToolCall, messages []model.Message) error {
	if s == nil {
		return nil
	}
	revealed := make(map[string]struct{})
	for _, name := range s.revealed(messages) {
		revealed[name] = struct{}{}
	}
	for _, call := range calls {
		if _, deferred := s.deferred[call.Name]; !deferred {
			continue
		}
		if _, ok := revealed[call.Name]; ok {
			continue
		}
		return &ToolError{ToolName: call.Name, CallID: call.ID,
			Err: fmt.Errorf("tool %q is deferred; call %s to reveal it", call.Name, SearchToolName)}
	}
	return nil
}

// render is the search result text: one line per match with the
// arguments the model needs to call it, or an explanation when nothing
// matched.
func (s *searchIndex) render(query string) string {
	matches := s.match(query)
	if len(matches) == 0 {
		return fmt.Sprintf("No tools matched %q; try different words or part of a tool name.",
			strings.TrimSpace(query))
	}
	var text strings.Builder
	fmt.Fprintf(&text, "Matched %d tool(s); they are available from now on:\n", len(matches))
	for _, match := range matches {
		fmt.Fprintf(&text, "- %s(%s)", match.name, strings.Join(match.args, ", "))
		if match.description != "" {
			fmt.Fprintf(&text, ": %s", match.description)
		}
		text.WriteByte('\n')
	}
	return strings.TrimSuffix(text.String(), "\n")
}

// searchToolSpec is the discovery tool's declaration, stable across
// turns so the advertised set only grows when a reveal lands.
func searchToolSpec() model.ToolSpec {
	return model.ToolSpec{
		Name: SearchToolName,
		Description: "Search the tools this agent can use but has not listed yet. " +
			"Returns matching tool names, their arguments, and what they do. " +
			"Matched tools become callable on the next turn.",
		Schema: searchToolSchema(),
	}
}

// searchToolSchema is the discovery tool's argument schema. It is a
// function so no request shares a mutable schema slice.
func searchToolSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Words describing the task, or part of a tool name."}},"required":["query"]}`)
}

// searchTool builds the discovery tool. It reads the same index the
// runner uses to advertise, so a result and the reveal it triggers can
// never disagree.
func searchTool[Deps any](index *searchIndex) tool.Tool[Deps] {
	spec := searchToolSpec()
	return tool.Tool[Deps]{
		Name:        spec.Name,
		Description: spec.Description,
		Schema:      spec.Schema,
		Exec: func(_ context.Context, _ Deps, args json.RawMessage) (tool.Result, error) {
			var input struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return tool.Result{}, &model.ModelRetry{Err: fmt.Errorf("search_tools arguments are not valid JSON: %w", err)}
			}
			if strings.TrimSpace(input.Query) == "" {
				return tool.Result{}, &model.ModelRetry{Err: errors.New("search_tools requires a non-empty query")}
			}
			return tool.Text(index.render(input.Query)), nil
		},
	}
}

// searchTokens splits s into lowercase alphanumeric tokens, breaking on
// separators and on camel-case boundaries so "mortgage_calculator" and
// "MortgageCalculator" tokenize alike.
func searchTokens(s string) []string {
	var tokens []string
	var current []rune
	flush := func() {
		if len(current) > 0 {
			tokens = append(tokens, strings.ToLower(string(current)))
			current = current[:0]
		}
	}
	var previous rune
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if unicode.IsUpper(r) && (unicode.IsLower(previous) || unicode.IsDigit(previous)) {
				flush()
			}
			current = append(current, r)
		default:
			flush()
		}
		previous = r
	}
	flush()
	return tokens
}

// uniqueTokens drops repeats so a query that says "rate rate" does not
// double a tool's score.
func uniqueTokens(tokens []string) []string {
	seen := make(map[string]struct{}, len(tokens))
	unique := tokens[:0]
	for _, token := range tokens {
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		unique = append(unique, token)
	}
	return unique
}

// schemaProperties lists a tool's declared argument names in sorted
// order, so the search result reads the same on every run.
func schemaProperties(schema json.RawMessage) []string {
	var document struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(schema, &document) != nil || len(document.Properties) == 0 {
		return nil
	}
	names := make([]string, 0, len(document.Properties))
	for name := range document.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
