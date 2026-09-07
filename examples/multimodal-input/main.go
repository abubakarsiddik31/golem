// Command multimodal-input attaches an inline image and an inline
// document to a run's prompt and asks the model to handle both. The
// image is a 1x1 red pixel and the document a one-page PDF the program
// builds itself, so the only network use is the model call.
//
// Set OPENAI_API_KEY (and optionally OPENAI_MODEL) to run it; the model
// must accept image and file inputs.
package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/providers/openai"
)

// redPixelPNG is a 1x1 red PNG; inline data keeps the example independent
// of any image host.
const redPixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC"

func main() {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Println("Set OPENAI_API_KEY (and optionally OPENAI_MODEL) to run this example.")
		return
	}
	modelName := os.Getenv("OPENAI_MODEL")
	if modelName == "" {
		modelName = "gpt-4o-mini"
	}

	client, err := openai.New(openai.Config{APIKey: apiKey, Model: modelName})
	if err != nil {
		fmt.Println("openai.New:", err)
		return
	}

	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(ctx context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}))
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	pixels, err := base64.StdEncoding.DecodeString(redPixelPNG)
	if err != nil {
		fmt.Println("decode embedded image:", err)
		return
	}
	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{},
		"What color is this image? Answer in one short sentence.",
		golem.WithPromptParts(model.ImageData("image/png", pixels)))
	if err != nil {
		fmt.Println("Run:", err)
		return
	}
	fmt.Println(result.Output)

	result, err = agent.Run(context.Background(), golem.RunContext[struct{}]{},
		"What text does this document contain? Answer in one short sentence.",
		golem.WithPromptParts(model.DocumentData("application/pdf", onePagePDF("Golem says hello."))))
	if err != nil {
		fmt.Println("Run:", err)
		return
	}
	fmt.Println(result.Output)
}

// onePagePDF builds a minimal but valid single-page PDF whose page shows
// text, so the example carries a real document without a test fixture.
func onePagePDF(text string) []byte {
	content := fmt.Sprintf("BT /F1 18 Tf 72 720 Td (%s) Tj ET",
		strings.ReplaceAll(strings.ReplaceAll(text, "\\", `\\`), ")", `\)`),
	)
	objects := []string{
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents 4 0 R/Resources<</Font<</F1 5 0 R>>>>>>",
		fmt.Sprintf("<</Length %d>>\nstream\n%s\nendstream", len(content), content),
		"<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>",
	}
	var pdf strings.Builder
	pdf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, object := range objects {
		offsets[i] = pdf.Len()
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := pdf.Len()
	fmt.Fprintf(&pdf, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&pdf, "trailer\n<</Size %d/Root 1 0 R>>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return []byte(pdf.String())
}
