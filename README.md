`go-remarkable-render` is a Go library for compositing reMarkable tablet background PDFs with vector `.rm` stroke layers to produce crisp, publication-quality PNG images.

# What It Does

- **Layered page compositing**: Merges stationery background PDFs with vector stylus handwriting and 24-bit Paper Pro color washes into standard PNG images.
- **Configurable resolution**: Renders composited pages at any target DPI (default 200 DPI for screen, 300+ for print).
- **Planner date indexing**: Reads the text of every page of an annual planner template and maps each calendar date (`YYYY-MM-DD`) to the 0-based indexes of its day and notes pages.
- **High-fidelity vector rasterization**: Combines `resvg-go` and `go-rmscene` for physics-based stylus dynamics and painter's order layering.
- **Flexible document inputs**: Accepts a `*Document` from `OpenDocumentFromBytes` for reuse across calls, raw byte slices, file paths, or readers.

# How It Works

- Loads the background PDF stationery and renders the requested page to a high-resolution bitmap.
- Decodes the corresponding `.rm` binary stroke file into layered vector SVG paths.
- Rasterizes the vector SVG layer at the exact pixel dimensions of the background bitmap.
- Composites the semi-transparent ink over the background using alpha blending and encodes the result as PNG.

# How it Really Works

- The compositing pipeline evaluates highlighters and shader washes beneath pen ink in painter's order, ensuring opaque pen handwriting remains sharp over color fills.
- PDFs are parsed, rendered, and read by PDFium compiled to WebAssembly and run in-process by the `wazero` runtime, and strokes are rasterized by `resvg` in WebAssembly the same way, so the library needs no cgo or system PDF libraries.
- A `*Document` from `OpenDocumentFromBytes` stays parsed until you call the cleanup function returned with it, so many pages can be rendered or read without reloading the PDF. A path, byte slice, or reader is read fully into memory, opened, and released again on every call.
- Open documents draw on a process-wide pool of at most four PDFium instances. Each open `*Document`, and each call given a path, byte slice, or reader, holds one instance until it is released; when all four are held, the next open waits up to 30 seconds and then returns an error.
- `RenderPlannerPage` returns the PNG as a byte slice and writes nothing to disk.

# Prerequisites

- [Go](https://go.dev/) 1.26.2 or newer, the minimum Go version declared by the `go` line in `go.mod`.

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
| `IndexPlannerDates` | `(pdfDocOrPath any, year int) (map[string]map[string]int, error)` | Reads the text of every page and maps each dated page to its 0-based page index in `map["YYYY-MM-DD"]["day"\|"notes"]`, writing `year` as `YYYY` in every key; pages are not filtered by year. A page counts when its text starts with a month abbreviation (`Jan` to `Dec`) and a day number (1 to 31) and contains a full weekday name such as `Thursday`: one containing the word `Notes` is the date's `day` page, otherwise one containing `Day` is its `notes` page. Every other page is skipped. Returns `ErrInvalidYear` when `year` is zero or negative. When the text of any page cannot be read, including from a `*Document` whose cleanup function was already called, returns an error naming the 0-based page index and no map |
| `OpenDocumentFromBytes` | `(data []byte) (*Document, func(), error)` | Opens a PDF for reuse across calls; call the returned function to release it. Returns an error, and releases everything it acquired, when the PDF cannot be opened or its page count cannot be read |
| `Document` | `struct` (unexported fields) | An open PDF held by one PDFium instance until the cleanup function from `OpenDocumentFromBytes` is called. Pass it as `pdfDocOrPath` to reuse it; the library never closes it |
| `Document.NumPage` | `() int` | Returns the number of pages, read once when the document is opened |
| `Document.Text` | `(pageIdx int) (string, error)` | Returns the plain text of a 0-based page, or `""` when the page has no text. Returns `ErrPageOutOfBounds` for an invalid index and an error when a PDFium request fails |
| `Document.Links` | `(pageIdx int) ([]PageLink, error)` | Returns the link annotations of a 0-based page in order. A link to a page of the document has its 0-based `TargetPage` and the URI `#page=N` (`TargetPage`+1). An external URI link has `TargetPage` `-1` and the URI PDFium reports: the raw bytes of its `/URI` string, not validated as UTF-8. When the catalog has a `/URI /Base` and the `/URI` string contains no `:` or starts with one, the base is concatenated in front of it; this is string concatenation, not RFC 3986 resolution, so it can produce a malformed URL. Without a `/Base`, a relative URI is returned as written. Any other link (remote go-to, launch, named, or unresolvable destination) has `TargetPage` `-1` and an empty URI, as does a URI action whose `/URI` string is empty in a document without a `/Base`. Each link also carries its `Rect` and the page `Text` inside it. A page without links returns an empty list; a failed PDFium request returns an error and no links |
| `PageLink` | `struct { Index, TargetPage int; URI string; Rect Rect; Text string }` | One link from `Document.Links`. `Text` is the page text PDFium reports inside `Rect`, in the page's text order, with every run of white space (including the line breaks PDFium inserts between text lines) replaced by one space and leading and trailing white space removed. A character whose box overlaps `Rect` counts in full even when only part of it is inside, while the rest of its word does not, so a rectangle that cuts through a word yields part of the word. `Text` is empty when the rectangle covers no text, such as a link over handwriting or an image |
| `Rect` | `struct { Left, Bottom, Right, Top float32 }` | A link annotation's `/Rect` in the page's default user space, as written in the PDF: points (1/72 inch unless the page sets `/UserUnit`), y growing upwards, without the page's `/MediaBox` or `/CropBox` origin or `/Rotate` applied; on a page whose media box starts at `[0 0]`, `(0, 0)` is the bottom-left corner. Fields follow the PDF array order `[Left Bottom Right Top]` and are normalized so that `Left <= Right` and `Bottom <= Top`. A missing `/Rect`, or one that is not a four-element array, is the zero `Rect` |
| `DefaultDPI` | `const int = 200` | Standard default rendering resolution in dots per inch |
| `ErrInvalidDocument` | `error` | Wrapped in the error `RenderPlannerPage` and `IndexPlannerDates` return when `pdfDocOrPath` is `nil`, a nil `*Document`, or a type other than `*Document`, `string`, `[]byte`, or `io.Reader`; match it with `errors.Is` |
| `ErrInvalidYear` | `error` | Returned by `IndexPlannerDates` when `year` is zero or negative |
| `ErrPageOutOfBounds` | `error` | Wrapped in the error `RenderPlannerPage`, `Document.Text`, and `Document.Links` return for a page index below 0 or at or above `NumPage()`; match it with `errors.Is` |

# Configuration

| Option | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `dpi` | `int` | `200` | Resolution for rasterizing background and vector layers; zero or a negative value uses `DefaultDPI` |
| `pdfDocOrPath` | `any` | required | Accepts a `*Document` from `OpenDocumentFromBytes`, a `string` file path, a `[]byte` PDF, or an `io.Reader`; anything else returns `ErrInvalidDocument` |

# License

MIT License (c) 2026 Alex Gorbatchev
