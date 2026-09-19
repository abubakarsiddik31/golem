// Command doc-extract demonstrates the docextract common tool in action:
// Word documents (.docx), Excel spreadsheets (.xlsx), PowerPoint decks (.pptx),
// Markdown (.md), CSVs, and PDFs are extracted into clean, agent-readable Markdown.
// Fully offline, zero external dependencies, and fast.
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abubakarsiddik31/golem"
	"github.com/abubakarsiddik31/golem/docextract"
	"github.com/abubakarsiddik31/golem/model"
	"github.com/abubakarsiddik31/golem/testmodel"
)

func main() {
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		docPath := os.Args[1]
		doc, err := docextract.Extract(context.Background(), docPath, docextract.Options{})
		if err != nil {
			fmt.Println("Extract error:", err)
			return
		}
		fmt.Printf("Extracted format: %s\nTitle: %s\nOutline items: %d\n\n", doc.Format, doc.Title, len(doc.Outline))
		fmt.Println("Content:\n" + doc.Content)
		return
	}

	root, err := os.MkdirTemp("", "golem-doc-extract")
	if err != nil {
		fmt.Println("MkdirTemp:", err)
		return
	}
	defer os.RemoveAll(root)

	// Create sample Word (.docx) document
	docxBytes := generateSampleDocx()
	docxPath := filepath.Join(root, "q1-report.docx")
	if err := os.WriteFile(docxPath, docxBytes, 0o644); err != nil {
		fmt.Println("WriteFile docx:", err)
		return
	}

	// Create sample Excel (.xlsx) sheet
	xlsxBytes := generateSampleXlsx()
	xlsxPath := filepath.Join(root, "budget.xlsx")
	if err := os.WriteFile(xlsxPath, xlsxBytes, 0o644); err != nil {
		fmt.Println("WriteFile xlsx:", err)
		return
	}

	// Create sample Markdown (.md) document with frontmatter
	mdContent := `---
author: Golem Agent Team
category: Strategy
---

# Agent Architecture 2026

Golem provides dependable, zero-dependency tools for AI agents.

## Core Pillars

- High performance and zero external dependencies
- Strict root-directory confinement and security
- Agent-readable Markdown output with intact tables
`
	mdPath := filepath.Join(root, "architecture.md")
	if err := os.WriteFile(mdPath, []byte(mdContent), 0o644); err != nil {
		fmt.Println("WriteFile md:", err)
		return
	}

	// Initialize docextract tool confined to root
	extractTool := docextract.MustNew[struct{}](docextract.Config{
		Root: root,
	})

	// Scripted agent runs multi-format extractions
	client := testmodel.New().Respond(
		// Step 1: Extract Word document outline
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{
				ID:   "call-1",
				Name: docextract.ToolName,
				Args: json.RawMessage(`{"path": "q1-report.docx", "outline": true}`),
			},
		}}},
		// Step 2: Extract Excel spreadsheet
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{
				ID:   "call-2",
				Name: docextract.ToolName,
				Args: json.RawMessage(`{"path": "budget.xlsx"}`),
			},
		}}},
		// Step 3: Extract Markdown section
		model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
			{
				ID:   "call-3",
				Name: docextract.ToolName,
				Args: json.RawMessage(`{"path": "architecture.md", "query": "Core Pillars"}`),
			},
		}}},
		// Final summary
		model.Response{Message: model.Message{Role: model.RoleAssistant, Content: "Successfully analyzed all documents across Word, Excel, and Markdown."}},
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

	result, err := agent.Run(context.Background(), golem.RunContext[struct{}]{}, "Extract and inspect all documents in workspace.")
	if err != nil {
		fmt.Println("agent.Run:", err)
		return
	}

	fmt.Println("Agent Final Output:", result.Output)
	fmt.Println("\n--- Documents Received by Agent ---")
	for _, m := range result.Messages {
		if m.Role == model.RoleTool {
			fmt.Printf("\n[Tool Output]:\n%s\n", m.Content)
		}
	}
}

func generateSampleDocx() []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	ct, _ := zw.Create("[Content_Types].xml")
	ct.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`))

	rels, _ := zw.Create("word/_rels/document.xml.rels")
	rels.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink" Target="https://github.com/abubakarsiddik31/golem" TargetMode="External"/>
</Relationships>`))

	docXml, _ := zw.Create("word/document.xml")
	docXml.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <w:body>
    <w:p>
      <w:pPr><w:pStyle w:val="Heading1"/></w:pPr>
      <w:r><w:t>Q1 Performance &amp; Strategy</w:t></w:r>
    </w:p>
    <w:p>
      <w:r><w:t>In the first quarter, the team achieved </w:t></w:r>
      <w:r><w:rPr><w:b/></w:rPr><w:t>outstanding results</w:t></w:r>
      <w:r><w:t> with key enterprise milestones met.</w:t></w:r>
    </w:p>
    <w:p>
      <w:pPr><w:numPr><w:ilvl w:val="0"/></w:numPr></w:pPr>
      <w:r><w:t>Delivered multi-format document extraction</w:t></w:r>
    </w:p>
    <w:p>
      <w:pPr><w:numPr><w:ilvl w:val="0"/></w:numPr></w:pPr>
      <w:r><w:t>Maintained zero-dependency Go core</w:t></w:r>
    </w:p>
    <w:p>
      <w:pPr><w:pStyle w:val="Heading2"/></w:pPr>
      <w:r><w:t>Key Metrics</w:t></w:r>
    </w:p>
    <w:tbl>
      <w:tr>
        <w:tc><w:p><w:r><w:t>Department</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>Target</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>Actual</w:t></w:r></w:p></w:tc>
      </w:tr>
      <w:tr>
        <w:tc><w:p><w:r><w:t>Engineering</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>100%</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>115%</w:t></w:r></w:p></w:tc>
      </w:tr>
    </w:tbl>
  </w:body>
</w:document>`))

	zw.Close()
	return buf.Bytes()
}

func generateSampleXlsx() []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	wb, _ := zw.Create("xl/workbook.xml")
	wb.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <sheets>
    <sheet name="Engineering Budget" sheetId="1" r:id="rId1"/>
  </sheets>
</workbook>`))

	rels, _ := zw.Create("xl/_rels/workbook.xml.rels")
	rels.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
</Relationships>`))

	sst, _ := zw.Create("xl/sharedStrings.xml")
	sst.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="4" uniqueCount="4">
  <si><t>Category</t></si>
  <si><t>Allocated</t></si>
  <si><t>Infrastructure</t></si>
  <si><t>Tooling</t></si>
</sst>`))

	s1, _ := zw.Create("xl/worksheets/sheet1.xml")
	s1.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <sheetData>
    <row r="1">
      <c r="A1" t="s"><v>0</v></c>
      <c r="B1" t="s"><v>1</v></c>
    </row>
    <row r="2">
      <c r="A2" t="s"><v>2</v></c>
      <c r="B2"><v>50000</v></c>
    </row>
    <row r="3">
      <c r="A3" t="s"><v>3</v></c>
      <c r="B3"><v>15000</v></c>
    </row>
  </sheetData>
</worksheet>`))

	zw.Close()
	return buf.Bytes()
}
