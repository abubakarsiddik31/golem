// Command prompt-template renders an agent's instructions through
// Go's text/template over typed dependencies — the standard-library
// answer to prompt templating. Golem adds no template engine of its
// own: instructions are a function of the run context, and a function
// can render a template. Offline against a scripted model.
package main

import (
	"context"
	"fmt"
	"strings"
	"text/template"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
)

// config is the agent's typed dependency value — the data a prompt
// template renders against.
type config struct {
	Product  string
	Tone     string
	Escalate bool
}

// policyTemplate is parsed once, at construction, so a broken template
// fails at startup instead of mid-run.
var policyTemplate = template.Must(template.New("policy").Parse(
	`You are the {{.Product}} support agent. Answer in a {{.Tone}} tone.
{{- if .Escalate}}
Refund requests above fifty dollars escalate to a human; say so plainly.
{{- else}}
You may approve refunds up to fifty dollars yourself.
{{- end}}`))

func main() {
	// Two agents over one template, differing only in the dependency
	// value each run carries: the prompt follows the deps, no rebuild.
	run("Comet", config{Product: "Comet", Tone: "patient", Escalate: true})
	run("Nimbus", config{Product: "Nimbus", Tone: "brisk", Escalate: false})
}

func newAgent() *golem.Agent[config, string] {
	m := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "Refunds follow the policy in my instructions."}},
	)
	agent, err := golem.New[config, string](m,
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
			return r.Message.Content, nil
		}),
		// The instructions function renders the template per run, so a
		// changed dependency value changes the prompt. Field-only
		// templates cannot fail at execute time; if one grows function
		// calls that can, surface the failure rather than send partial
		// guidance.
		golem.WithInstructionsFunc[config, string](func(_ context.Context, runCtx golem.RunContext[config]) string {
			var rendered strings.Builder
			if err := policyTemplate.Execute(&rendered, runCtx.Deps); err != nil {
				return "Instructions are temporarily unavailable: " + err.Error()
			}
			return rendered.String()
		}),
	)
	if err != nil {
		panic(err) // static configuration; nothing else can fail here
	}
	return agent
}

func run(name string, deps config) {
	agent := newAgent()
	result, err := agent.Run(context.Background(), golem.RunContext[config]{Deps: deps},
		"Can I get a refund?")
	if err != nil {
		fmt.Println("Run:", err)
		return
	}
	// The scripted model records every request, so the example can show
	// the instructions exactly as the provider will see them: the first
	// message of the request is the rendered system guidance.
	system := result.Messages[0]
	fmt.Printf("%s instructions:\n  %s\n", name, strings.ReplaceAll(system.Content, "\n", "\n  "))
	fmt.Printf("%s answer: %s\n\n", name, result.Output)
}
