# AGENTS.md

Instructions, architectural rules, and test verification standards for agents working on `go-remarkable-render`.

---

## Repository Purpose

`go-remarkable-render` (`github.com/alexgorbatchev/go-remarkable-render`) is a high-performance Go library for compositing reMarkable tablet stationery background PDFs with vector `.rm` stroke layers to high-resolution PNGs. It provides:

1. **Planner Date Indexing (`IndexPlannerDates`)**:
   Scans reMarkable planner template PDF pages, extracts text with `go-fitz` (MuPDF), and indexes dates matching `"Month Day Weekday Notes/Day"` into a two-level map: `map[YYYY-MM-DD]map[day|notes]pageIdx`.
2. **Layered Page Compositing (`RenderPlannerPage`)**:
   Renders background template PDF pages at a target DPI using `go-fitz`. When vector stroke bytes (`.rm`) are provided, converts strokes into layered SVG via `go-rmscene`, rasterizes them to exact pixel dimensions using `resvg-go` (WASM resvg engine), and composites the stroke layer over the PDF background with `image/draw`.

---

## Core Dependencies

- `github.com/alexgorbatchev/go-rmscene`: reMarkable version 6 `.rm` binary stroke parser and layered vector SVG generator.
- `github.com/gen2brain/go-fitz`: High-speed MuPDF rendering engine in Go for PDF rasterization and text extraction.
- `github.com/kanrichan/resvg-go`: High-fidelity SVG rasterizer using resvg compiled to WebAssembly via Wazero.

---

## Architectural Rules & Invariants

### 1. Flexible Document Input Contract
Functions accepting `pdfDocOrPath` (`RenderPlannerPage`, `IndexPlannerDates`) MUST accept:
- `*fitz.Document`: Caller-managed active document. The library MUST NOT close this document.
- `string`: File path on disk. Opened via `fitz.New`, closed on return.
- `[]byte`: In-memory PDF byte slice. Opened via `fitz.NewFromMemory`, closed on return.
- `io.Reader`: Streaming reader. Opened via `fitz.NewFromReader`, closed on return.
Any other type or `nil` must return `ErrInvalidDocument`.

### 2. Exact Pixel Alignment & Compositing
- When rasterizing stroke SVGs with `resvg-go`, the dimensions passed to `renderer.RenderWithSize(svg, widthPx, heightPx)` MUST match the exact pixel bounds of the rasterized PDF background (`bgImg.Bounds().Dx()`, `bgImg.Bounds().Dy()`).
- Alpha blending is performed using `image/draw.Draw(bgImg, bounds, strokeImg, image.Point{}, draw.Over)` to composite transparent vector ink on top of background stationery.

### 3. Date Indexing Semantics
- Daily planner pages have navigation links pairing each date's views:
  - Header token `"Notes"` identifies the **Day** view (`kind = "day"`), linking to its paired notes page.
  - Header token `"Day"` identifies the **Notes** view (`kind = "notes"`), linking back to its paired day page.
- Date string keys are formatted as ISO 8601 `YYYY-MM-DD`.

### 4. Empty Stroke Handling
- If `rmBytes` is `nil` or empty (`len(rmBytes) == 0`), `RenderPlannerPage` skips stroke parsing and rasterization, returning the encoded PNG of the background PDF page directly.

---

## Testing & Verification Standards

### 1. Running Tests
Run the test suite using `just`:
```sh
just test
# Or directly:
go test -v ./...
```

Run linter / checks:
```sh
just check
```

### 2. Code Coverage
- 90% statement code coverage is required across exported packages.
- Run `go test -coverprofile=coverage.out && go tool cover -func=coverage.out` to verify coverage.

### 3. Required Test Suites
- `TestIndexPlannerDates_Success`: Multi-page synthetic PDF fixture validating date parsing, day/notes mapping, and non-matching page skips.
- `TestIndexPlannerDates_Errors`: Validates negative/zero year, nil document, unsupported types, corrupt files.
- `TestRenderPlannerPage_EmptyStrokes`: Validates nil and zero-length byte slices produce valid PNGs matching target DPI dimensions.
- `TestRenderPlannerPage_SampleStrokes`: Constructs v6 `.rm` streams with ballpoint and Paper Pro highlighter strokes, verifying composited PNG pixel differences against background-only renders.
- `TestRenderPlannerPage_DocumentInputs`: Tests `*fitz.Document`, file path, and `io.Reader` inputs.
- `TestRenderPlannerPage_Errors`: Tests invalid page bounds, malformed `.rm` bytes, corrupt PDFs, and unsupported types.
