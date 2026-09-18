// Command pdf-extract demonstrates the pdfextract common tool in action:
// a generated multi-column PDF with an intact table and an image placeholder
// is extracted by a scripted agent — fully offline, deterministic, and fast.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/pdfextract"
	"github.com/abubakarsiddik31/golem/testmodel"
)

func main() {
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		pdfPath := os.Args[1]
		doc, err := pdfextract.Extract(context.Background(), pdfPath, pdfextract.Options{})
		if err != nil {
			fmt.Println("Extract error:", err)
			return
		}
		fmt.Printf("Extracted %d pages, %d tables, %d images:\n\n", len(doc.Pages), len(doc.Tables), len(doc.Images))
		fmt.Println(doc.Markdown)
		return
	}

	root, err := os.MkdirTemp("", "golem-pdf-extract")
	if err != nil {
		fmt.Println("MkdirTemp:", err)
		return
	}
	defer os.RemoveAll(root)

	// Create a sample PDF file with a heading, multi-column layout, and a ruled table
	pdfBytes := generateSamplePDF()
	pdfPath := filepath.Join(root, "report.pdf")
	if err := os.WriteFile(pdfPath, pdfBytes, 0o644); err != nil {
		fmt.Println("WriteFile:", err)
		return
	}

	// Tool is confined to Root
	extractTool := pdfextract.MustNew[struct{}](pdfextract.Config{
		Root: root,
	})

	// Scripted agent requests PDF extraction
	client := testmodel.New().Respond(
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{
				ID:   "call-1",
				Name: pdfextract.ToolName,
				Args: json.RawMessage(`{"path": "report.pdf"}`),
			},
		}}},
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "Extracted document successfully with intact table and reading order."}},
	)

	agent, err := golem.New[struct{}, string](client,
		golem.DecodeFunc[string](func(_ context.Context, response model.Response) (string, error) {
			return response.Message.Content, nil
		}),
		golem.WithTools[struct{}, string](extractTool),
	)
	if err != nil {
		fmt.Println("golem.New:", err)
		return
	}

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "Extract report.pdf")
	if err != nil {
		fmt.Println("agent.Run:", err)
		return
	}

	fmt.Println("Agent result:", result.Output)
	fmt.Println("\nExtracted Markdown received by the agent:")
	for _, m := range result.Messages {
		if m.Role == model.RoleTool {
			fmt.Println(m.Content)
		}
	}
}

func generateSamplePDF() []byte {
	var objects []string
	objects = append(objects, "<</Type/Catalog/Pages 2 0 R>>")
	objects = append(objects, "<</Type/Pages/Kids[3 0 R]/Count 1>>")
	objects = append(objects, "<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents 4 0 R/Resources<</Font<</F1 5 0 R>>>>>>")

	var content strings.Builder
	// Heading 1
	content.WriteString("BT /F1 22 Tf 50 720 Td (Quarterly Financial Report) Tj ET\n")
	// Left Column
	content.WriteString("BT /F1 11 Tf 50 670 Td (Revenue increased across all segments.) Tj ET\n")
	content.WriteString("BT /F1 11 Tf 50 650 Td (Operating margin expanded with efficiency.) Tj ET\n")
	// Right Column
	content.WriteString("BT /F1 11 Tf 350 670 Td (Cloud infrastructure drove highest margin.) Tj ET\n")
	content.WriteString("BT /F1 11 Tf 350 650 Td (International operations showed steady growth.) Tj ET\n")
	// Ruled Table
	content.WriteString("50 580 m 350 580 l S\n50 555 m 350 555 l S\n50 530 m 350 530 l S\n")
	content.WriteString("50 530 m 50 580 l S\n200 530 m 200 580 l S\n350 530 m 350 580 l S\n")
	content.WriteString("BT /F1 10 Tf 60 565 Td (Segment) Tj ET\n")
	content.WriteString("BT /F1 10 Tf 210 565 Td (Revenue) Tj ET\n")
	content.WriteString("BT /F1 10 Tf 60 540 Td (Cloud Services) Tj ET\n")
	content.WriteString("BT /F1 10 Tf 210 540 Td ($42.5M) Tj ET\n")

	contentStr := content.String()
	objects = append(objects, fmt.Sprintf("<</Length %d>>\nstream\n%sendstream", len(contentStr), contentStr))
	objects = append(objects, "<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>")

	var sb strings.Builder
	sb.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, obj := range objects {
		offsets[i] = sb.Len()
		fmt.Fprintf(&sb, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xrefPos := sb.Len()
	fmt.Fprintf(&sb, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&sb, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&sb, "trailer\n<</Size %d/Root 1 0 R>>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xrefPos)

	return []byte(sb.String())
}
