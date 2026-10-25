// Command tool-search shows a large tool catalog kept out of the prompt
// until the model asks for it. Nine tools are deferred; the agent
// advertises one eager tool plus a synthesized search_tools tool. The
// scripted model searches for the invoice tool, Golem reveals it, and the
// next request carries its schema.
//
// It runs offline against testmodel: no network and no credentials.
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
	"github.com/abubakarsiddik31/golem/tool"
)

// catalogEntry is one declared tool: name, prose for the search index,
// and the argument schema the model sees once the tool is revealed.
type catalogEntry struct {
	name        string
	description string
	schema      json.RawMessage
}

func main() {
	eager := catalogEntry{
		name:        "get_current_time",
		description: "Report the current time in a time zone.",
		schema:      json.RawMessage(`{"type":"object","properties":{"zone":{"type":"string"}}}`),
	}
	deferred := []catalogEntry{
		{name: "get_weather", description: "Report the current weather for a city.",
			schema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`)},
		{name: "get_invoice", description: "Read an invoice by its id.",
			schema: json.RawMessage(`{"type":"object","properties":{"invoice_id":{"type":"string"}}}`)},
		{name: "issue_refund", description: "Refund a charge on a customer order.",
			schema: json.RawMessage(`{"type":"object","properties":{"order_id":{"type":"string"}}}`)},
		{name: "track_shipment", description: "Track a parcel by its tracking number.",
			schema: json.RawMessage(`{"type":"object","properties":{"tracking_number":{"type":"string"}}}`)},
		{name: "cancel_subscription", description: "Cancel a customer subscription plan.",
			schema: json.RawMessage(`{"type":"object","properties":{"plan_id":{"type":"string"}}}`)},
		{name: "reset_password", description: "Send a password reset link to an account.",
			schema: json.RawMessage(`{"type":"object","properties":{"account_id":{"type":"string"}}}`)},
		{name: "check_inventory", description: "Count units in stock for a product.",
			schema: json.RawMessage(`{"type":"object","properties":{"sku":{"type":"string"}}}`)},
		{name: "get_tax_rate", description: "Look up the sales tax rate for a region.",
			schema: json.RawMessage(`{"type":"object","properties":{"region":{"type":"string"}}}`)},
		{name: "book_meeting", description: "Reserve a meeting room for a time slot.",
			schema: json.RawMessage(`{"type":"object","properties":{"slot":{"type":"string"}}}`)},
	}

	deferredTools := make([]tool.Tool[struct{}], len(deferred))
	for i, entry := range deferred {
		deferredTools[i] = buildTool(entry)
	}

	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "search-1", Name: "search_tools", Args: json.RawMessage(`{"query":"invoice"}`)},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{ID: "invoice-1", Name: "get_invoice", Args: json.RawMessage(`{"invoice_id":"INV-42"}`)},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "Invoice INV-42 is paid: $120.00."}},
	)

	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithInstructions[struct{}, string]("Search for a tool before using one that is not listed."),
		golem.WithTools[struct{}, string](buildTool(eager)),
		golem.WithDeferredTools[struct{}, string](deferredTools...),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "What does invoice INV-42 say?")
	if err != nil {
		fmt.Println("Run:", err)
		return
	}

	fmt.Printf("catalog: %d eager, %d deferred\n", 1, len(deferred))
	for i, request := range client.Requests() {
		fmt.Printf("request %d advertises %d tool(s): %v\n", i, len(request.ToolSpecs), specNames(request.ToolSpecs))
	}
	for _, message := range result.Messages {
		if message.Role == model.RoleTool {
			fmt.Printf("tool %s returned %q\n", message.ToolName, message.Content)
		}
	}
	fmt.Println("answer:", result.Output)
}

func buildTool(entry catalogEntry) tool.Tool[struct{}] {
	return tool.MustNew(tool.Tool[struct{}]{
		Name:        entry.name,
		Description: entry.description,
		Schema:      entry.schema,
		Exec: func(context.Context, struct{}, json.RawMessage) (tool.Result, error) {
			switch entry.name {
			case "get_invoice":
				return tool.Text("INV-42: $120.00, paid in full"), nil
			default:
				return tool.Text(entry.name + " ok"), nil
			}
		},
	})
}

func specNames(specs []model.ToolSpec) []string {
	names := make([]string, len(specs))
	for i, spec := range specs {
		names[i] = spec.Name
	}
	return names
}
