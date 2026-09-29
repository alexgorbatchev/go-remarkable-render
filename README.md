# go-remarkable-render

[![Go Reference](https://pkg.go.dev/badge/github.com/alexgorbatchev/go-remarkable-render.svg)](https://pkg.go.dev/github.com/alexgorbatchev/go-remarkable-render)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A high-performance Go library for compositing **reMarkable tablet stationery background PDFs** with vector **`.rm` stroke layers** to produce crisp, publication-quality **PNG images**.

Designed for reMarkable 1, reMarkable 2, and **reMarkable Paper Pro** daily planners, notebooks, and documents.

---

## Features

- **Layered Page Compositing**: Combines background template PDFs with stylus handwriting, sketches, and 24-bit Paper Pro highlighter strokes into a single composited PNG.
- **Configurable DPI**: Render pages at any target resolution (default 200 DPI, or 150, 300, 600 DPI for print).
- **Planner Date Indexing**: Automatically scans multi-hundred page planner template PDFs to index daily pages and notes pages by ISO date (`YYYY-MM-DD`).
- **Flexible Document Inputs**: Functions accept `*fitz.Document` (for batch reuse), file path (`string`), `[]byte`, or `io.Reader`.
- **High-Fidelity Vector Rasterization**: Leverages `resvg-go` (WASM resvg engine) and `go-rmscene` for physics-based stylus dynamics and painter's order layering.

---

## Installation

```sh
go get github.com/alexgorbatchev/go-remarkable-render
```

---

## Architecture & Rendering Pipeline

```
  ┌───────────────────────────┐      ┌───────────────────────────┐
  │   Background PDF Template │      │   Binary .rm Stroke Data  │
  └─────────────┬─────────────┘      └─────────────┬─────────────┘
                │                                  │
    [go-fitz: ImageDPI]             [go-rmscene: RenderRMToSVG]
                │                                  │
                ▼                                  ▼
      *image.RGBA Background              Layered Vector SVG
                │                                  │
                │                        [resvg-go: RenderWithSize]
                │                                  │
                │                                  ▼
                │                          Rasterized Stroke PNG
                │                                  │
                └───────────────┬──────────────────┘
                                │
                      [image/draw: Draw.Over]
                                │
                                ▼
                       Composited *image.RGBA
                                │
                         [png: Encode]
                                │
                                ▼
                       Final Encoded PNG Bytes
```

1. **Background Rasterization**: The requested PDF page is rendered to an `*image.RGBA` bitmap at the specified target DPI using `go-fitz` (MuPDF).
2. **Vector Stroke Parsing**: Binary v6 `.rm` data is parsed and converted to a layered SVG using `go-rmscene`, placing highlighter strokes underneath ink in painter's order.
3. **Exact Pixel Rasterization**: The SVG is rasterized with `resvg-go` at the exact pixel dimensions (`Dx` x `Dy`) of the rendered background image.
4. **Alpha Compositing**: `image/draw.Draw(..., draw.Over)` blends the semi-transparent vector ink layer directly over the stationery background.
5. **PNG Encoding**: The final composite is encoded and returned as standard PNG bytes.

---

## Usage

### 1. Indexing Planner Template Dates

Scan a reMarkable planner PDF (such as a 500+ page year planner) to map each date to its respective "day" and "notes" page indices:

```go
package main

import (
	"fmt"
	"log"

	"github.com/alexgorbatchev/go-remarkable-render"
)

func main() {
	// Index planner pages for year 2026
	// Accepts file path, []byte, io.Reader, or *fitz.Document
	index, err := render.IndexPlannerDates("planner_2026.pdf", 2026)
	if err != nil {
		log.Fatalf("failed to index planner: %v", err)
	}

	today := "2026-09-28"
	if pages, ok := index[today]; ok {
		fmt.Printf("Date: %s\n", today)
		fmt.Printf("  Day View Page Index:   %d\n", pages["day"])
		fmt.Printf("  Notes View Page Index: %d\n", pages["notes"])
	}
}
```

### 2. Compositing a Planner Page with Handwritten Strokes

Render a specific planner page with its associated `.rm` stroke file:

```go
package main

import (
	"log"
	"os"

	"github.com/alexgorbatchev/go-remarkable-render"
)

func main() {
	pdfPath := "planner_2026.pdf"
	pageIndex := 193 // e.g. September 28, 2026

	// Read binary .rm stroke file downloaded from reMarkable Cloud
	rmBytes, err := os.ReadFile("193.rm")
	if err != nil {
		log.Fatalf("failed to read .rm file: %v", err)
	}

	// Render composited PNG at 200 DPI
	pngBytes, err := render.RenderPlannerPage(pdfPath, pageIndex, rmBytes, 200)
	if err != nil {
		log.Fatalf("failed to render planner page: %v", err)
	}

	if err := os.WriteFile("2026-09-28-day.png", pngBytes, 0644); err != nil {
		log.Fatalf("failed to write output PNG: %v", err)
	}
}
```

### 3. Rendering Background Only (Empty Strokes)

Pass `nil` or an empty byte slice `[]byte{}` for `rmBytes` to render the clean template stationery:

```go
pngBytes, err := render.RenderPlannerPage(pdfPath, pageIndex, nil, 200)
if err != nil {
	log.Fatalf("failed to render template: %v", err)
}
```

### 4. High-Throughput Batch Processing with `*fitz.Document`

When rendering multiple pages, keep a single `*fitz.Document` open to eliminate repeated PDF parsing overhead:

```go
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/alexgorbatchev/go-remarkable-render"
	"github.com/gen2brain/go-fitz"
)

func main() {
	doc, err := fitz.New("planner_2026.pdf")
	if err != nil {
		log.Fatalf("failed to open PDF: %v", err)
	}
	defer doc.Close()

	// Index dates once using the active document
	index, err := render.IndexPlannerDates(doc, 2026)
	if err != nil {
		log.Fatal(err)
	}

	// Render multiple pages reusing the same document instance
	for date, pages := range index {
		rmBytes, _ := os.ReadFile(fmt.Sprintf("strokes/%s.rm", date))
		pngBytes, err := render.RenderPlannerPage(doc, pages["day"], rmBytes, 200)
		if err != nil {
			log.Printf("failed to render %s: %v", date, err)
			continue
		}
		os.WriteFile(fmt.Sprintf("rendered/%s-day.png", date), pngBytes, 0644)
	}
}
```

---

## API Reference

### `IndexPlannerDates(pdfDocOrPath any, year int) (map[string]map[string]int, error)`

Scans PDF pages, reads text via MuPDF, and matches headers formatted like:
`"Month Day Weekday Notes/Day"`

- Returns `map[string]map[string]int` mapping ISO date strings (`"YYYY-MM-DD"`) to a map containing `"day"` and/or `"notes"` keys with their respective zero-indexed page numbers.
- `pdfDocOrPath` accepts `*fitz.Document`, `string` (file path), `[]byte`, or `io.Reader`.

### `RenderPlannerPage(pdfDocOrPath any, pageIdx int, rmBytes []byte, dpi int) ([]byte, error)`

Renders the background PDF page at `dpi` (defaults to 200 if `<= 0`).
If `rmBytes` is non-empty:
1. Translates `.rm` strokes into layered SVG via `go-rmscene`.
2. Rasterizes the SVG to exact pixel dimensions using `resvg-go`.
3. Composites the stroke layer over the background using `image/draw`.

Returns the encoded PNG bytes.

---

## Dependencies & Attribution

This library builds upon outstanding open-source projects:

- **[go-rmscene](https://github.com/alexgorbatchev/go-rmscene)**: reMarkable v6 `.rm` stroke parser and layered SVG renderer by Alex Gorbatchev.
- **[go-fitz](https://github.com/gen2brain/go-fitz)**: Go wrapper for the MuPDF rendering engine by Miroslav Kovac.
- **[resvg-go](https://github.com/kanrichan/resvg-go)**: WebAssembly bindings for the resvg rendering library by kanrichan, powered by [Wazero](https://wazero.io/).
- **[rmscene](https://github.com/ricklupton/rmscene) / [rmc](https://github.com/ricklupton/rmc)**: Foundational reverse-engineering of the reMarkable v6 binary format by Rick Lupton.

---

## License

MIT License &mdash; see [LICENSE](LICENSE) for details.

Copyright (c) 2026 Alex Gorbatchev.
