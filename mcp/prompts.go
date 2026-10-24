package mcp

import (
	"context"
	"errors"
	"fmt"
)

// Prompt is one prompt template a server publishes, as reported by
// prompts/list. A prompt is application-driven: the host chooses to
// render it, typically as a conversation's opening turn, so it is not
// bridged into a tool the model invokes on its own authority.
type Prompt struct {
	// Name identifies the prompt. It is the argument GetPrompt takes.
	Name string `json:"name"`
	// Title is the human-readable name, when the server supplies one.
	Title string `json:"title"`
	// Description explains what the prompt is for.
	Description string `json:"description"`
	// Arguments lists the parameters the template accepts.
	Arguments []PromptArgument `json:"arguments"`
}

// PromptArgument is one parameter a prompt template accepts.
type PromptArgument struct {
	// Name is the argument's key in the GetPrompt arguments map.
	Name string `json:"name"`
	// Title is the human-readable name, when the server supplies one.
	Title string `json:"title"`
	// Description explains what the argument means.
	Description string `json:"description"`
	// Required reports whether the template needs the argument.
	Required bool `json:"required"`
}

// PromptResult is the rendered prompt a server returns from
// prompts/get: a description and the messages the application can add to
// its own history.
type PromptResult struct {
	// Description explains the rendered prompt, when the server
	// supplies one.
	Description string `json:"description"`
	// Messages is the rendered conversation, in server order.
	Messages []PromptMessage `json:"messages"`
}

// PromptMessage is one rendered message. Role is "user" or "assistant",
// as the protocol defines them.
type PromptMessage struct {
	Role    string        `json:"role"`
	Content PromptContent `json:"content"`
}

// PromptContent is one content block of a prompt message. Type names the
// protocol's union member: "text" fills Text, "resource" fills Resource,
// and "image" and "audio" fill Data and MIMEType. Other members — such as
// resource_link — are reported by Type alone.
type PromptContent struct {
	// Type is the content block's kind.
	Type string `json:"type"`
	// Text is the block's text for a text block.
	Text string `json:"text"`
	// MIMEType is the media type of image and audio data.
	MIMEType string `json:"mimeType"`
	// Data is the decoded payload of an image or audio block.
	Data []byte `json:"data"`
	// Resource is the embedded resource of a resource block.
	Resource *ResourceContent `json:"resource"`
}

// Text renders the message content as text: a text block's text, a text
// resource's payload, or one placeholder line naming the block type. It
// is convenience for prompts built from text; inspect Content directly
// for anything else.
func (m PromptMessage) Text() string {
	switch {
	case m.Content.Type == "text":
		return m.Content.Text
	case m.Content.Type == "resource" && m.Content.Resource != nil && m.Content.Resource.Text != "":
		return m.Content.Resource.Text
	default:
		return fmt.Sprintf("[mcp: unsupported %s content]", m.Content.Type)
	}
}

// ListPrompts returns the server's complete prompt list, following
// pagination cursors until the list is exhausted.
func (c *Client) ListPrompts(ctx context.Context) ([]Prompt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ready {
		return nil, errNotInitialized
	}
	return paginate(ctx, func(cursor string) ([]Prompt, string, error) {
		var result struct {
			Prompts    []Prompt `json:"prompts"`
			NextCursor string   `json:"nextCursor"`
		}
		if err := c.call(ctx, "prompts/list", pageCursor{Cursor: cursor}, &result); err != nil {
			return nil, "", err
		}
		return result.Prompts, result.NextCursor, nil
	})
}

// GetPrompt renders one prompt template with the given arguments. A nil
// map sends no arguments. An empty name fails locally instead of sending
// a request the server can only reject.
func (c *Client) GetPrompt(ctx context.Context, name string, arguments map[string]string) (PromptResult, error) {
	if name == "" {
		return PromptResult{}, errors.New("mcp: prompt name is empty")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ready {
		return PromptResult{}, errNotInitialized
	}
	params := struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments,omitempty"`
	}{Name: name, Arguments: arguments}
	var result PromptResult
	if err := c.call(ctx, "prompts/get", params, &result); err != nil {
		return PromptResult{}, err
	}
	return result, nil
}
