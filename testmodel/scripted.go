// Package testmodel provides model implementations for testing agents
// without provider credentials or network access.
//
// Scripted plays queued outcomes in order and records every request it
// received. Func adapts a function to a plain model.Model; StreamFunc
// adapts one that streams. Tests stay deterministic: no provider, no
// clock, no environment.
package testmodel

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/abubakarsiddik31/golem/model"
)

// step is one scripted outcome: a response or a failure.
type step struct {
	response model.Response
	err      error
}

// Scripted is a model.Model — and a model.StreamingModel — that plays
// queued outcomes in order and records every request it receives. Queue
// outcomes with Respond and Fail; inspect what the agent sent with
// Requests. It is built for tests: it is not safe for concurrent use, and
// an exhausted queue fails generation instead of blocking.
type Scripted struct {
	steps    []step
	requests []model.Request
}

var _ model.StreamingModel = (*Scripted)(nil)

// New returns an empty Scripted model; queue outcomes with Respond and
// Fail.
func New() *Scripted {
	return &Scripted{}
}

// Respond queues successful responses, returned in order by later calls.
func (m *Scripted) Respond(responses ...model.Response) *Scripted {
	for _, response := range responses {
		m.steps = append(m.steps, step{response: response})
	}
	return m
}

// Fail queues failures, returned in order by later calls.
func (m *Scripted) Fail(failures ...error) *Scripted {
	for _, err := range failures {
		m.steps = append(m.steps, step{err: err})
	}
	return m
}

// Generate returns the next queued outcome and records the request.
func (m *Scripted) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	m.requests = append(m.requests, cloneRequest(request))
	if len(m.steps) == 0 {
		return model.Response{}, errors.New("testmodel: no queued outcome; queue one with Respond or Fail")
	}
	next := m.steps[0]
	m.steps = m.steps[1:]
	return next.response, next.err
}

// GenerateStream draws from the same queue as Generate, records the
// request the same way, and replays the response as fragments before
// returning it: one thinking fragment per visible thinking block, one
// content delta when the response carries text, then one identifying
// fragment per tool call, indexed by position — the order providers
// stream them in. Redacted blocks have no stream representation and ride
// only in the returned response, as on the wire. A non-nil error from
// onDelta stops the replay and is returned as-is.
func (m *Scripted) GenerateStream(ctx context.Context, request model.Request, onDelta func(model.Delta) error) (model.Response, error) {
	response, err := m.Generate(ctx, request)
	if err != nil {
		return model.Response{}, err
	}
	for i, block := range response.Message.Thinking {
		if block.Text == "" {
			continue
		}
		delta := model.Delta{Thinking: []model.ThinkingDelta{{Index: i, Text: block.Text, Signature: block.Signature}}}
		if err := Emit(onDelta, delta); err != nil {
			return model.Response{}, err
		}
	}
	if response.Message.Content != "" {
		if err := Emit(onDelta, model.Delta{Content: response.Message.Content}); err != nil {
			return model.Response{}, err
		}
	}
	for i, call := range response.Message.ToolCalls {
		delta := model.Delta{ToolCalls: []model.ToolCallDelta{{Index: i, ID: call.ID, Name: call.Name, Signature: call.Signature}}}
		if err := Emit(onDelta, delta); err != nil {
			return model.Response{}, err
		}
	}
	return response, nil
}

// Requests returns the requests the model has received, in order. The
// returned slice is a copy: mutating it does not rewrite the recording.
func (m *Scripted) Requests() []model.Request {
	requests := make([]model.Request, len(m.requests))
	for i, request := range m.requests {
		requests[i] = cloneRequest(request)
	}
	return requests
}

// cloneRequest copies the request's slices so later mutation by the caller
// does not rewrite recorded evidence.
func cloneRequest(request model.Request) model.Request {
	messages := make([]model.Message, len(request.Messages))
	for i, message := range request.Messages {
		messages[i] = cloneMessage(message)
	}
	specs := make([]model.ToolSpec, len(request.ToolSpecs))
	for i, spec := range request.ToolSpecs {
		specs[i] = spec
		specs[i].Schema = append(json.RawMessage(nil), spec.Schema...)
	}
	request.Messages = messages
	request.ToolSpecs = specs
	request.OutputSchema = append(json.RawMessage(nil), request.OutputSchema...)
	return request
}

func cloneMessage(message model.Message) model.Message {
	message.ToolCalls = append([]model.ToolCall(nil), message.ToolCalls...)
	for i := range message.ToolCalls {
		message.ToolCalls[i].Args = append(json.RawMessage(nil), message.ToolCalls[i].Args...)
	}
	message.Parts = cloneParts(message.Parts)
	message.Thinking = append([]model.ThinkingBlock(nil), message.Thinking...)
	return message
}

// cloneParts copies parts and their inline data so recorded evidence
// cannot be rewritten through a mutated alias.
func cloneParts(parts []model.Part) []model.Part {
	if parts == nil {
		return nil
	}
	copied := make([]model.Part, len(parts))
	for i, part := range parts {
		if len(part.Data) > 0 {
			data := make([]byte, len(part.Data))
			copy(data, part.Data)
			part.Data = data
		}
		copied[i] = part
	}
	return copied
}
