package render

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"

	"github.com/alexgorbatchev/go-rmscene"
	"github.com/kanrichan/resvg-go"
	"github.com/klippa-app/go-pdfium/requests"
)

const (
	// DefaultDPI is the standard rasterization density (200 DPI).
	DefaultDPI = 200
)

// RenderPlannerPage renders a specific background PDF page at the target DPI.
// If rmBytes is provided (non-empty), it converts the .rm stroke data to SVG using
// rmscene.RenderRMToSVG, rasterizes the SVG to exact page pixel dimensions with resvg-go,
// and composites the stroke layer over the PDF background using image/draw.
// Returns the final composited image as encoded PNG bytes.
//
// Accepts *Document, string (file path), []byte, or io.Reader for pdfDocOrPath.
func RenderPlannerPage(pdfDocOrPath any, pageIdx int, rmBytes []byte, dpi int) ([]byte, error) {
	if dpi <= 0 {
		dpi = DefaultDPI
	}

	doc, cleanup, err := resolveDocument(pdfDocOrPath)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	if pageIdx < 0 || pageIdx >= doc.NumPage() {
		return nil, fmt.Errorf("%w: pageIdx %d (document has %d pages)", ErrPageOutOfBounds, pageIdx, doc.NumPage())
	}

	// 1. Render background PDF page to image at target DPI
	renderResp, err := pdfiumResponse(doc.instance.RenderPageInDPI(&requests.RenderPageInDPI{
		Page: requests.Page{
			ByIndex: &requests.PageByIndex{
				Document: doc.handle.Document,
				Index:    pageIdx,
			},
		},
		DPI: dpi,
	}))
	if err != nil {
		return nil, fmt.Errorf("failed to render PDF page %d at %d DPI: %w", pageIdx, dpi, err)
	}
	// In WebAssembly the image is a view of the bitmap in the instance's
	// memory, and only Cleanup destroys that bitmap. Deferring it here
	// releases the bitmap on every return path, after the last read of bgImg,
	// so a reused *Document does not accumulate one bitmap per render.
	defer renderResp.Cleanup()
	bgImg := renderResp.Result.RenderedImage

	// 2. If no rm stroke bytes are provided, encode and return the background PDF page directly
	if len(rmBytes) == 0 {
		var buf bytes.Buffer
		if err := png.Encode(&buf, bgImg); err != nil {
			return nil, fmt.Errorf("failed to encode background PNG: %w", err)
		}
		return buf.Bytes(), nil
	}

	// 3. Obtain page dimensions in points for SVG viewport
	pageSizeResp, err := doc.instance.GetPageSize(&requests.GetPageSize{
		Page: requests.Page{
			ByIndex: &requests.PageByIndex{
				Document: doc.handle.Document,
				Index:    pageIdx,
			},
		},
	})
	var widthPt, heightPt float64
	if err == nil && pageSizeResp != nil && pageSizeResp.Width > 0 && pageSizeResp.Height > 0 {
		widthPt = pageSizeResp.Width
		heightPt = pageSizeResp.Height
	} else {
		widthPt = rmscene.DefaultWidthPt
		heightPt = rmscene.DefaultHeightPt
	}

	// 4. Render .rm bytes to layered SVG
	svgStr, err := rmscene.RenderRMToSVG(rmBytes, rmscene.WithDimensions(widthPt, heightPt))
	if err != nil {
		return nil, fmt.Errorf("failed to render rm strokes to SVG: %w", err)
	}

	// 5. Rasterize SVG to exact background pixel dimensions
	bgBounds := bgImg.Bounds()
	widthPx := uint32(bgBounds.Dx())
	heightPx := uint32(bgBounds.Dy())

	strokePng, err := rasterizeSVG([]byte(svgStr), widthPx, heightPx)
	if err != nil {
		return nil, err
	}

	strokeImg, err := png.Decode(bytes.NewReader(strokePng))
	if err != nil {
		return nil, fmt.Errorf("failed to decode stroke PNG: %w", err)
	}

	// 6. Composite strokes over background image
	finalImg := image.NewRGBA(bgBounds)
	draw.Draw(finalImg, bgBounds, bgImg, bgBounds.Min, draw.Src)
	draw.Draw(finalImg, bgBounds, strokeImg, bgBounds.Min, draw.Over)

	// 7. Encode final composited image to PNG
	var out bytes.Buffer
	if err := png.Encode(&out, finalImg); err != nil {
		return nil, fmt.Errorf("failed to encode composited PNG: %w", err)
	}

	return out.Bytes(), nil
}

// rasterizeSVG renders svg to PNG bytes of exactly widthPx by heightPx pixels
// in a resvg WebAssembly runtime that lives only for this call. The runtime is
// closed on every path, and a failure to close it is returned.
//
// The renderer is not closed on its own: resvg-go v0.0.1's Renderer.Close
// calls the module's __renderer_delete export without the renderer pointer it
// requires, so it always fails with "expected 1 params, but passed 0" and frees
// nothing. The renderer lives in the runtime's module memory, which closing the
// runtime releases.
func rasterizeSVG(svg []byte, widthPx, heightPx uint32) (pngBytes []byte, err error) {
	rctx, err := resvg.NewContext(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to create resvg context: %w", err)
	}
	defer func() {
		if closeErr := rctx.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("closing resvg context: %w", closeErr))
		}
	}()

	renderer, err := rctx.NewRenderer()
	if err != nil {
		return nil, fmt.Errorf("failed to create resvg renderer: %w", err)
	}

	pngBytes, err = renderer.RenderWithSize(svg, widthPx, heightPx)
	if err != nil {
		return nil, fmt.Errorf("failed to rasterize stroke SVG: %w", err)
	}
	return pngBytes, nil
}
