# AGENTS.md

Instructions, architectural rules, and test verification standards for agents working on `go-remarkable-render`.

---

## Repository Purpose

`go-remarkable-render` (`github.com/alexgorbatchev/go-remarkable-render`) is a high-performance Go library for compositing reMarkable tablet stationery background PDFs with vector `.rm` stroke layers to high-resolution PNGs. It provides:

1. **Planner Date Indexing (`IndexPlannerDates`)**:
   Scans reMarkable planner template PDF pages, extracts each page's text with PDFium (`GetPageText`), and indexes dates matching `"Month Day Weekday Notes/Day"` into a two-level map: `map[YYYY-MM-DD]map[day|notes]pageIdx`.
2. **Layered Page Compositing (`RenderPlannerPage`)**:
   Renders background template PDF pages at a target DPI with PDFium (`RenderPageInDPI`). When vector stroke bytes (`.rm`) are provided, converts strokes into layered SVG via `go-rmscene`, rasterizes them to exact pixel dimensions using `resvg-go` (WASM resvg engine), and composites the stroke layer over the PDF background with `image/draw`.

---

## Core Dependencies

- `github.com/alexgorbatchev/go-rmscene`: reMarkable version 6 `.rm` binary stroke parser and layered vector SVG generator.
- `github.com/klippa-app/go-pdfium`: PDFium compiled to WebAssembly and run in-process by `github.com/tetratelabs/wazero` (no cgo), used through its `webassembly` pool for PDF rasterization, page text, and link extraction.
- `github.com/kanrichan/resvg-go`: High-fidelity SVG rasterizer using resvg compiled to WebAssembly via Wazero. In v0.0.1, `Renderer.Close` calls the `__renderer_delete` export without the renderer pointer, so it always fails and frees nothing. Never call it, and never discard its error to satisfy errcheck; the renderer is released with the module memory when the `resvg.Context` is closed.

---

## Architectural Rules & Invariants

### 1. Flexible Document Input Contract
Functions accepting `pdfDocOrPath` (`RenderPlannerPage`, `IndexPlannerDates`) MUST accept:
- `*Document`: Caller-managed document from `OpenDocumentFromBytes`. The library MUST NOT close this document; the caller releases it with the cleanup function returned alongside it.
- `string`: File path on disk. Read with `os.ReadFile`, opened via `OpenDocumentFromBytes`, closed on return.
- `[]byte`: In-memory PDF byte slice. Opened via `OpenDocumentFromBytes`, closed on return.
- `io.Reader`: Streaming reader. Read with `io.ReadAll`, opened via `OpenDocumentFromBytes`, closed on return.
Any other type, `nil`, or a nil `*Document` must return `ErrInvalidDocument`.

### 2. Exact Pixel Alignment & Compositing
- When rasterizing stroke SVGs with `resvg-go`, the dimensions passed to `renderer.RenderWithSize(svg, widthPx, heightPx)` MUST match the exact pixel bounds of the rasterized PDF background (`bgImg.Bounds().Dx()`, `bgImg.Bounds().Dy()`).
- Composite into a new image, never into `bgImg`. Copy the background into `finalImg := image.NewRGBA(bgBounds)` with `draw.Draw(finalImg, bgBounds, bgImg, bgBounds.Min, draw.Src)`, then alpha-blend the transparent vector ink over it with `draw.Draw(finalImg, bgBounds, strokeImg, bgBounds.Min, draw.Over)`, and encode `finalImg`.
- `bgImg` is a write-through view of the PDFium bitmap in the instance's WebAssembly memory (rule 5), so drawing onto it writes into PDFium's memory. The `draw.Src` copy is the last read of `bgImg` on the stroke path, which is what lets the deferred `Cleanup()` run after the composite is encoded without corrupting it.

### 3. Date Indexing Semantics
- Daily planner pages have navigation links pairing each date's views:
  - Header token `"Notes"` identifies the **Day** view (`kind = "day"`), linking to its paired notes page.
  - Header token `"Day"` identifies the **Notes** view (`kind = "notes"`), linking back to its paired day page.
- Date string keys are formatted as ISO 8601 `YYYY-MM-DD`.
- Read page text through `Document.Text`. A failed text request on any page, including a nil PDFium response or a released `*Document`, returns that error with the 0-based page index and a nil map; never skip the page or return a partial index. A page whose text was read is skipped only when it lacks a matching `Month Day Weekday` header with `Notes` or `Day`.

### 4. Empty Stroke Handling
- If `rmBytes` is `nil` or empty (`len(rmBytes) == 0`), `RenderPlannerPage` skips stroke parsing and rasterization, returning the encoded PNG of the background PDF page directly.

### 5. PDFium Render Response Lifetime
- A go-pdfium render response (`RenderPageInDPI` and the other responses with a `CleanupFunc`) holds a bitmap in the PDFium instance's WebAssembly memory, and its `RenderedImage` is a view of that memory, not a copy. Only `Cleanup()` destroys the bitmap; closing the instance frees it only because the pool does not reuse workers.
- Call `Cleanup()` exactly once for every successful render, on every return path, after the last read of the image (the PNG encode or the `draw.Draw` onto the composite). `defer renderResp.Cleanup()` right after the error check does both. A missed cleanup leaks one full-page bitmap per render in a reused `*Document` until PDFium can no longer allocate one; a cleanup before the last read corrupts the image.
- Check render responses with `pdfiumResponse`, like every other PDFium request, so a nil response is an error rather than a nil dereference.

---

## Testing & Verification Standards

### 1. Running Tests
Run the full gate before declaring a change verified:
```sh
just check   # just lint, then just test
```

- `just lint`: `go mod tidy -diff`, `go vet ./...`, and `golangci-lint run ./...` with the linters pinned in `.golangci.yml` (`errcheck`, `govet`, `ineffassign`, `staticcheck`, `unused`) and the `gofmt` formatter, which fails on any file `gofmt -s` would change. Install the golangci-lint binary at the version pinned in `.github/workflows/ci.yml`.
- `just fmt`: `golangci-lint fmt ./...`, which rewrites files with the same `gofmt -s` formatting the lint gate checks. `go fmt` omits `-s` and leaves simplifications that fail the gate.
- `just test`: `go test -race -v ./...`.

CI runs the same checks: the `verify` job runs tidy, build, vet, and the race-enabled tests, and the `golangci-lint` job runs golangci-lint through `golangci/golangci-lint-action`.

### 2. Code Coverage
- 90% statement code coverage is required across exported packages.
- Run `go test -coverprofile=coverage.out && go tool cover -func=coverage.out` to verify coverage.

### 3. Required Test Suites
- `TestIndexPlannerDates_Success`: Multi-page synthetic PDF fixture validating date parsing, day/notes mapping, and non-matching page skips.
- `TestIndexPlannerDates_Errors`: Validates negative/zero year, nil document, unsupported types, corrupt files.
- `TestIndexPlannerDates_PageTextFailures`: Injects an error and a nil response into `GetPageText` for a page after an indexed one, asserting a wrapped error naming that page and a nil map.
- `TestIndexPlannerDates_ReleasedDocumentFails`: Indexes a `*Document` after its cleanup function ran, asserting PDFium's closed-instance error for page 0 and a nil map.
- `TestRenderPlannerPage_EmptyStrokes`: Validates nil and zero-length byte slices produce valid PNGs matching target DPI dimensions.
- `TestRenderPlannerPage_SampleStrokes`: Constructs v6 `.rm` streams with ballpoint and Paper Pro highlighter strokes, verifying composited PNG pixel differences against background-only renders.
- `TestRenderPlannerPage_DocumentInputs`: Tests `*Document`, file path, and `io.Reader` inputs.
- `TestRenderPlannerPage_Errors`: Tests invalid page bounds, malformed `.rm` bytes, corrupt PDFs, and unsupported types.
- `TestRenderPlannerPage_ReusedDocumentReleasesBitmaps`: Renders one page repeatedly through a single `*Document` on a PDFium instance whose memory is capped with wazero's `WithMemoryLimitPages`, so any leaked bitmap makes a later render fail to allocate.
- `TestRenderPlannerPage_ReleasesRenderedPage`: Wraps the instance in `faultInjectingPdfium` to assert that each rendered page is released exactly once on success, malformed-stroke, injected-error, and nil-response paths, and that the image is not read after release (the wrapper clears its pixels on release, and the result must stay opaque).
