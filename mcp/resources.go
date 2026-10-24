package mcp

import (
	"context"
	"errors"
)

// Resource is one resource a server publishes, as reported by
// resources/list. A resource is application-driven: the host decides
// whether, when, and how to load it, so it is not bridged into a tool.
type Resource struct {
	// URI identifies the resource. It is the argument ReadResource
	// takes.
	URI string `json:"uri"`
	// Name is the programmatic name.
	Name string `json:"name"`
	// Title is the human-readable name, when the server supplies one.
	Title string `json:"title"`
	// Description explains what the resource holds.
	Description string `json:"description"`
	// MIMEType is the content type, when the server declares one.
	MIMEType string `json:"mimeType"`
	// Size is the payload size in bytes; zero means the server did not
	// report one.
	Size int64 `json:"size"`
}

// ResourceTemplate is one parameterized resource family, as reported by
// resources/templates/list. URITemplate contains the protocol's RFC 6570
// placeholders; expanding it is the application's job.
type ResourceTemplate struct {
	// URITemplate is the template with its placeholders.
	URITemplate string `json:"uriTemplate"`
	// Name is the programmatic name.
	Name string `json:"name"`
	// Title is the human-readable name, when the server supplies one.
	Title string `json:"title"`
	// Description explains what the template produces.
	Description string `json:"description"`
	// MIMEType is the content type, when the server declares one.
	MIMEType string `json:"mimeType"`
}

// ResourceContent is one piece of a resource read. Exactly one payload
// member is set: Text for a text resource, Blob for a binary one. The
// protocol's Base64 blob string decodes into Blob automatically.
type ResourceContent struct {
	// URI identifies the resource this content came from.
	URI string `json:"uri"`
	// MIMEType is the content type, when the server declares one.
	MIMEType string `json:"mimeType"`
	// Text is the payload of a text resource.
	Text string `json:"text"`
	// Blob is the decoded payload of a binary resource.
	Blob []byte `json:"blob"`
}

// ListResources returns the server's complete resource list, following
// pagination cursors until the list is exhausted.
func (c *Client) ListResources(ctx context.Context) ([]Resource, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ready {
		return nil, errNotInitialized
	}
	return paginate(ctx, func(cursor string) ([]Resource, string, error) {
		var result struct {
			Resources  []Resource `json:"resources"`
			NextCursor string     `json:"nextCursor"`
		}
		if err := c.call(ctx, "resources/list", pageCursor{Cursor: cursor}, &result); err != nil {
			return nil, "", err
		}
		return result.Resources, result.NextCursor, nil
	})
}

// ListResourceTemplates returns the server's complete resource-template
// list, following pagination cursors until the list is exhausted.
func (c *Client) ListResourceTemplates(ctx context.Context) ([]ResourceTemplate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ready {
		return nil, errNotInitialized
	}
	return paginate(ctx, func(cursor string) ([]ResourceTemplate, string, error) {
		var result struct {
			ResourceTemplates []ResourceTemplate `json:"resourceTemplates"`
			NextCursor        string             `json:"nextCursor"`
		}
		if err := c.call(ctx, "resources/templates/list", pageCursor{Cursor: cursor}, &result); err != nil {
			return nil, "", err
		}
		return result.ResourceTemplates, result.NextCursor, nil
	})
}

// ReadResource reads one resource by URI. The result is a list because
// one URI may resolve to several pieces — a directory-like resource, for
// instance. An empty URI fails locally instead of sending a request the
// server can only reject.
func (c *Client) ReadResource(ctx context.Context, uri string) ([]ResourceContent, error) {
	if uri == "" {
		return nil, errors.New("mcp: resource URI is empty")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ready {
		return nil, errNotInitialized
	}
	params := struct {
		URI string `json:"uri"`
	}{URI: uri}
	var result struct {
		Contents []ResourceContent `json:"contents"`
	}
	if err := c.call(ctx, "resources/read", params, &result); err != nil {
		return nil, err
	}
	return result.Contents, nil
}
