// Command content-filter shows the two policies for a provider safety
// stop: by default the response is ordinary output, and with
// golem.WithContentFilterError the run fails with a typed error. It runs
// offline against a scripted model.
package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
)

func main() {
	response := model.Response{
		Message:      model.Message{Role: model.RoleAssistant, Content: "I cannot help with that."},
		FinishReason: model.FinishContentFilter,
	}

	defaultAgent, err := golem.New[struct{}, string](testmodel.New().Respond(response),
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
			return r.Message.Content, nil
		}))
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}
	result, err := defaultAgent.Run(context.Background(), golem.RunContext[struct{}]{}, "prompt")
	if err != nil {
		fmt.Println("default run:", err)
		return
	}
	fmt.Printf("default: output=%q finish=%s\n", result.Output, result.FinishReason)

	strictAgent, err := golem.New[struct{}, string](testmodel.New().Respond(response),
		golem.DecodeFunc[string](func(_ context.Context, r model.Response) (string, error) {
			return r.Message.Content, nil
		}),
		golem.WithContentFilterError[struct{}, string](),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}
	_, err = strictAgent.Run(context.Background(), golem.RunContext[struct{}]{}, "prompt")

	var runErr *golem.RunError
	var filterErr *golem.ContentFilterError
	switch {
	case !errors.As(err, &runErr):
		fmt.Println("strict run did not return a RunError:", err)
	case !errors.As(err, &filterErr):
		fmt.Println("strict run did not carry a ContentFilterError:", err)
	default:
		fmt.Printf("strict: stage=%s filtered=%q\n", runErr.Stage, filterErr.Message.Content)
		if runErr.Partial != nil {
			fmt.Printf("strict: %d partial messages preserved\n", len(runErr.Partial.Messages))
		}
	}
}
