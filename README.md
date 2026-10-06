`go-remarkable-render` is a Go library for compositing reMarkable tablet background PDFs with vector `.rm` stroke layers to produce crisp, publication-quality PNG images.

# What It Does

- **Layered page compositing**: Merges stationery background PDFs with vector stylus handwriting and 24-bit Paper Pro color washes into standard PNG images.
- **Configurable resolution**: Renders composited pages at any target DPI (default 200 DPI for screen, 300+ for print).
- **Planner date indexing**: Scans internal PDF link annotations across annual planner templates to map calendar dates (`YYYY-MM-DD`) to page numbers.
- **High-fidelity vector rasterization**: Combines `resvg-go` and `go-rmscene` for physics-based stylus dynamics and painter's order layering.
- **Flexible document inputs**: Accepts `*fitz.Document` pointers for fast batch reuse, raw byte slices, file paths, or readers.

# How It Works

- Loads the background PDF stationery and renders the requested page to a high-resolution bitmap.
- Decodes the corresponding `.rm` binary stroke file into layered vector SVG paths.
- Rasterizes the vector SVG layer at the exact pixel dimensions of the background bitmap.
- Composites the semi-transparent ink over the background using alpha blending and encodes the result as PNG.

# How it Really Works

- The compositing pipeline evaluates highlighters and shader washes beneath pen ink in painter's order, ensuring opaque pen handwriting remains sharp over color fills.
- When reusing an existing `*fitz.Document` pointer, MuPDF page structures remain in memory, allowing hundreds of pages to be rendered in batch without reloading the underlying PDF.
- Memory allocations during rendering are recycled per page, and output PNGs are written directly to memory buffers or disk without intermediate disk artifacts.
- The library is safe for concurrent use across multiple goroutines when each goroutine operates on its own document instances or uses read-only document pointers.

# Prerequisites

- [Go](https://go.dev/) 1.26 or newer.

# Installation

```bash
go get github.com/alexgorbatchev/go-remarkable-render
```

# Quick Start

```go
package main

import (
	"os"

	render "github.com/alexgorbatchev/go-remarkable-render"
)

func main() {
	pdfBytes, err := os.ReadFile("template.pdf")
	if err != nil {
		panic(err)
	}

	rmBytes, err := os.ReadFile("page.rm")
	if err != nil {
		panic(err)
	}

	// Composite page 0 at 200 DPI
	pngBytes, err := render.RenderPlannerPage(pdfBytes, 0, rmBytes, 200)
	if err != nil {
		panic(err)
	}

	_ = os.WriteFile("rendered.png", pngBytes, 0644)
}
```

# API

| Export | Signature | Description |
| :--- | :--- | :--- |
| `RenderPlannerPage` | `(pdfDocOrPath any, pageIdx int, rmBytes []byte, dpi int) ([]byte, error)` | Composites a page with strokes to PNG bytes |
| `IndexPlannerDates` | `(pdfDocOrPath any, year int) (map[string]map[string]int, error)` | Indexes annual planner PDF dates to page indices |
| `OpenDocumentFromBytes` | `(data []byte) (*Document, func(), error)` | Opens a PDF for reuse across calls; call the returned function to release it. Returns an error, and releases everything it acquired, when the PDF cannot be opened or its page count cannot be read |
| `Document.Text` | `(pageIdx int) (string, error)` | Returns the plain text of a 0-based page, or `""` when the page has no text. Returns `ErrPageOutOfBounds` for an invalid index and an error when a PDFium request fails |
| `Document.Links` | `(pageIdx int) ([]PageLink, error)` | Returns the link annotations of a 0-based page in order. A link to a page of the document has its 0-based `TargetPage` and the URI `#page=N` (`TargetPage`+1). An external URI link has `TargetPage` `-1` and the URI PDFium reports: the raw bytes of its `/URI` string, not validated as UTF-8. When the catalog has a `/URI /Base` and the `/URI` string contains no `:` or starts with one, the base is concatenated in front of it; this is string concatenation, not RFC 3986 resolution, so it can produce a malformed URL. Without a `/Base`, a relative URI is returned as written. Any other link (remote go-to, launch, named, or unresolvable destination) has `TargetPage` `-1` and an empty URI, as does a URI action whose `/URI` string is empty in a document without a `/Base`. Each link also carries its `Rect` and the page `Text` inside it. A page without links returns an empty list; a failed PDFium request returns an error and no links |
| `PageLink` | `struct { Index, TargetPage int; URI string; Rect Rect; Text string }` | One link from `Document.Links`. `Text` is the page text PDFium reports inside `Rect`, in the page's text order, with every run of white space (including the line breaks PDFium inserts between text lines) replaced by one space and leading and trailing white space removed. A character whose box overlaps `Rect` counts in full even when only part of it is inside, while the rest of its word does not, so a rectangle that cuts through a word yields part of the word. `Text` is empty when the rectangle covers no text, such as a link over handwriting or an image |
| `Rect` | `struct { Left, Bottom, Right, Top float32 }` | A link annotation's `/Rect` in the page's default user space, as written in the PDF: points (1/72 inch unless the page sets `/UserUnit`), y growing upwards, without the page's `/MediaBox` or `/CropBox` origin or `/Rotate` applied; on a page whose media box starts at `[0 0]`, `(0, 0)` is the bottom-left corner. Fields follow the PDF array order `[Left Bottom Right Top]` and are normalized so that `Left <= Right` and `Bottom <= Top`. A missing `/Rect`, or one that is not a four-element array, is the zero `Rect` |
| `DefaultDPI` | `const int = 200` | Standard default rendering resolution in dots per inch |

# Configuration

| Option | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `dpi` | `int` | `200` | Resolution for rasterizing background and vector layers |
| `pdfDocOrPath` | `any` | required | Accepts `*fitz.Document`, `string` (path), `[]byte`, or `io.Reader` |

# License

MIT License (c) 2026 Alex Gorbatchev
