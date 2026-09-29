package render

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/png"

	"github.com/alexgorbatchev/go-rmscene"
	"github.com/kanrichan/resvg-go"
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
// Accepts *fitz.Document, string (file path), []byte, or io.Reader for pdfDocOrPath.
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
	bgImg, err := doc.ImageDPI(pageIdx, float64(dpi))
	if err != nil {
		return nil, fmt.Errorf("failed to render PDF page %d at %d DPI: %w", pageIdx, dpi, err)
	}

	// 2. If no rm stroke bytes are provided, encode and return the background PDF page directly
	if len(rmBytes) == 0 {
		var buf bytes.Buffer
		if err := png.Encode(&buf, bgImg); err != nil {
			return nil, fmt.Errorf("failed to encode background PNG: %w", err)
		}
		return buf.Bytes(), nil
	}

	// 3. Obtain page dimensions in points for SVG viewport
	rect, err := doc.Bound(pageIdx)
	var widthPt, heightPt float64
	if err == nil && rect.Dx() > 0 && rect.Dy() > 0 {
		widthPt = float64(rect.Dx())
		heightPt = float64(rect.Dy())
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

	ctx, err := resvg.NewContext(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to create resvg context: %w", err)
	}
	defer ctx.Close()

	renderer, err := ctx.NewRenderer()
	if err != nil {
		return nil, fmt.Errorf("failed to create resvg renderer: %w", err)
	}
	defer renderer.Close()

	strokePng, err := renderer.RenderWithSize([]byte(svgStr), widthPx, heightPx)
	if err != nil {
		return nil, fmt.Errorf("failed to rasterize stroke SVG: %w", err)
	}

	strokeImg, err := png.Decode(bytes.NewReader(strokePng))
	if err != nil {
		return nil, fmt.Errorf("failed to decode stroke PNG: %w", err)
	}

	// 6. Composite stroke layer over background using image/draw
	draw.Draw(bgImg, bgBounds, strokeImg, image.Point{}, draw.Over)

	// 7. Encode final composited image to PNG
	var buf bytes.Buffer
	if err := png.Encode(&buf, bgImg); err != nil {
		return nil, fmt.Errorf("failed to encode composited PNG: %w", err)
	}
	return buf.Bytes(), nil
}
