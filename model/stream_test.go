package model_test

import (
	"context"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
)

// generateOnly fakes a Model with no streaming capability, like every
// test fake and simple adapter.
type generateOnly struct{}

func (m *generateOnly) Generate(ctx context.Context, request model.Request) (model.Response, error) {
	return model.Response{}, nil
}

// streamingFake fakes a StreamingModel: Generate plus GenerateStream.
type streamingFake struct{ generateOnly }

func (m *streamingFake) GenerateStream(ctx context.Context, request model.Request, onDelta func(model.Delta) error) (model.Response, error) {
	return model.Response{}, nil
}

// TestStreamingIsAnOptionalCapability pins the port's core promise: a
// Generate-only implementation still satisfies Model, and
// adding GenerateStream opts a model into StreamingModel.
func TestStreamingIsAnOptionalCapability(t *testing.T) {
	t.Parallel()

	var plain model.Model = &generateOnly{}
	if _, ok := plain.(model.StreamingModel); ok {
		t.Fatal("Generate-only model must not satisfy StreamingModel")
	}

	var streaming model.StreamingModel = &streamingFake{}
	// StreamingModel embeds Model, so the streaming value is usable
	// everywhere a Model is.
	var asModel model.Model = streaming
	if asModel == nil {
		t.Fatal("StreamingModel must satisfy Model")
	}
}
