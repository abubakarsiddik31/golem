package gemini_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
)

func TestToolResultCarriesPartsAndFailure(t *testing.T) {
	recorder := newRecordedServer(200, `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}],"usageMetadata":{"totalTokenCount":1}}`)
	client := newClient(t, recorder.server.URL)

	_, err := client.Generate(context.Background(), model.Request{Messages: []model.Message{
		{Role: model.RoleUser, Content: "look"},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "call-1", Name: "camera"}}},
		{Role: model.RoleTool, ToolCallID: "call-1", ToolName: "camera", Content: "no such file", Failed: true,
			Parts: []model.Part{model.ImageData("image/png", []byte{1})}},
	}})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	body := recorder.last(t).body
	var sent struct {
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				FunctionResponse *struct {
					Name     string          `json:"name"`
					Response json.RawMessage `json:"response"`
				} `json:"functionResponse,omitempty"`
				Text string `json:"text,omitempty"`
			} `json:"parts"`
		} `json:"contents"`
	}
	if err := json.Unmarshal([]byte(body), &sent); err != nil {
		t.Fatalf("request body is not valid JSON: %v", err)
	}

	var response *struct {
		Name     string
		Response string
	}
	var framed []string
	for _, turn := range sent.Contents {
		for _, part := range turn.Parts {
			if part.FunctionResponse != nil {
				response = &struct {
					Name     string
					Response string
				}{part.FunctionResponse.Name, string(part.FunctionResponse.Response)}
			}
			if part.Text != "" && strings.Contains(part.Text, "tool_result") {
				framed = append(framed, part.Text)
			}
		}
	}
	if response == nil {
		t.Fatalf("no functionResponse part in %s", body)
	}
	if response.Name != "camera" || !strings.Contains(response.Response, `"error"`) {
		t.Fatalf("functionResponse = %#v, want the camera call's failure", response)
	}
	if len(framed) == 0 || !strings.Contains(body, "inlineData") {
		t.Fatalf("framed media parts missing: %s", body)
	}
}
