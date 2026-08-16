package golem

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/abubakarsiddik31/golem/model"
)

// DecodeJSON returns an OutputDecoder that decodes the final response's
// message content as JSON into Output. Content that is not valid JSON for
// Output is rejected as *model.ModelRetry — a correctable rejection — so
// with an output retry budget configured the run asks the model to fix
// the response instead of failing. Pair it with WithOutputSchema so the
// model is told the expected shape up front.
func DecodeJSON[Output any]() OutputDecoder[Output] {
	return DecodeFunc[Output](func(_ context.Context, response model.Response) (Output, error) {
		var output Output
		if err := json.Unmarshal([]byte(response.Message.Content), &output); err != nil {
			return output, &model.ModelRetry{
				Err: fmt.Errorf("response content is not valid JSON for %T: %w", output, err),
			}
		}
		return output, nil
	})
}
