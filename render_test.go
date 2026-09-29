package render_test

import (
	"bytes"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/go-remarkable-render"
	"github.com/gen2brain/go-fitz"
)

func TestRenderPlannerPage_EmptyStrokes(t *testing.T) {
	pdfBytes := buildMinimalPDF(
		[]string{"Jan 15 Wednesday Notes", "Top Priority Item"},
	)

	// Test with nil rmBytes
	pngNil, err := render.RenderPlannerPage(pdfBytes, 0, nil, 150)
	if err != nil {
		t.Fatalf("RenderPlannerPage with nil rmBytes failed: %v", err)
	}

	imgNil, err := png.Decode(bytes.NewReader(pngNil))
	if err != nil {
		t.Fatalf("failed to decode PNG from nil rmBytes: %v", err)
	}
	if imgNil.Bounds().Dx() <= 0 || imgNil.Bounds().Dy() <= 0 {
		t.Fatalf("invalid image dimensions: %v", imgNil.Bounds())
	}

	// Test with empty []byte{}
	pngEmpty, err := render.RenderPlannerPage(pdfBytes, 0, []byte{}, 150)
	if err != nil {
		t.Fatalf("RenderPlannerPage with empty slice rmBytes failed: %v", err)
	}

	imgEmpty, err := png.Decode(bytes.NewReader(pngEmpty))
	if err != nil {
		t.Fatalf("failed to decode PNG from empty rmBytes: %v", err)
	}
	if imgEmpty.Bounds() != imgNil.Bounds() {
		t.Fatalf("bounds mismatch between nil and empty rmBytes: %v vs %v", imgEmpty.Bounds(), imgNil.Bounds())
	}

	// Test default DPI when dpi <= 0
	pngDefaultDPI, err := render.RenderPlannerPage(pdfBytes, 0, nil, 0)
	if err != nil {
		t.Fatalf("RenderPlannerPage with dpi=0 failed: %v", err)
	}
	imgDefaultDPI, err := png.Decode(bytes.NewReader(pngDefaultDPI))
	if err != nil {
		t.Fatalf("failed to decode PNG with default DPI: %v", err)
	}
	// At 200 DPI, dimensions should be larger than 150 DPI
	if imgDefaultDPI.Bounds().Dx() <= imgNil.Bounds().Dx() {
		t.Errorf("expected 200 DPI image width (%d) to be greater than 150 DPI (%d)",
			imgDefaultDPI.Bounds().Dx(), imgNil.Bounds().Dx())
	}
}

func TestRenderPlannerPage_SampleStrokes(t *testing.T) {
	pdfBytes := buildMinimalPDF(
		[]string{"Jan 15 Wednesday Notes", "Background Template Content"},
	)

	rmBytes, err := buildSampleRMStrokes()
	if err != nil {
		t.Fatalf("failed to build sample RM strokes: %v", err)
	}

	// Render background only
	bgOnlyPng, err := render.RenderPlannerPage(pdfBytes, 0, nil, 100)
	if err != nil {
		t.Fatalf("RenderPlannerPage background-only failed: %v", err)
	}

	// Render with stroke overlay
	compositedPng, err := render.RenderPlannerPage(pdfBytes, 0, rmBytes, 100)
	if err != nil {
		t.Fatalf("RenderPlannerPage with sample strokes failed: %v", err)
	}

	// Decode both images
	bgImg, err := png.Decode(bytes.NewReader(bgOnlyPng))
	if err != nil {
		t.Fatalf("failed to decode background PNG: %v", err)
	}
	compImg, err := png.Decode(bytes.NewReader(compositedPng))
	if err != nil {
		t.Fatalf("failed to decode composited PNG: %v", err)
	}

	if bgImg.Bounds() != compImg.Bounds() {
		t.Fatalf("image bounds mismatch: bg=%v comp=%v", bgImg.Bounds(), compImg.Bounds())
	}

	// Verify pixel difference: composited image must contain drawn strokes
	differ := false
	bounds := bgImg.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r1, g1, b1, a1 := bgImg.At(x, y).RGBA()
			r2, g2, b2, a2 := compImg.At(x, y).RGBA()
			if r1 != r2 || g1 != g2 || b1 != b2 || a1 != a2 {
				differ = true
				break
			}
		}
		if differ {
			break
		}
	}

	if !differ {
		t.Fatal("composited PNG is identical to background-only PNG; stroke layers were not rendered")
	}
}

func TestRenderPlannerPage_DocumentInputs(t *testing.T) {
	pdfBytes := buildMinimalPDF([]string{"Test Page"})
	rmBytes, err := buildSampleRMStrokes()
	if err != nil {
		t.Fatalf("failed to build sample strokes: %v", err)
	}

	// 1. *fitz.Document
	doc, err := fitz.NewFromMemory(pdfBytes)
	if err != nil {
		t.Fatalf("fitz.NewFromMemory failed: %v", err)
	}
	defer doc.Close()

	pngDoc, err := render.RenderPlannerPage(doc, 0, rmBytes, 100)
	if err != nil {
		t.Fatalf("render from *fitz.Document failed: %v", err)
	}
	if len(pngDoc) == 0 {
		t.Fatal("empty PNG from *fitz.Document")
	}

	// 2. File path
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.pdf")
	if err := os.WriteFile(filePath, pdfBytes, 0644); err != nil {
		t.Fatalf("failed to write temp PDF: %v", err)
	}

	pngFile, err := render.RenderPlannerPage(filePath, 0, rmBytes, 100)
	if err != nil {
		t.Fatalf("render from file path failed: %v", err)
	}
	if len(pngFile) == 0 {
		t.Fatal("empty PNG from file path")
	}

	// 3. io.Reader
	pngReader, err := render.RenderPlannerPage(bytes.NewReader(pdfBytes), 0, rmBytes, 100)
	if err != nil {
		t.Fatalf("render from io.Reader failed: %v", err)
	}
	if len(pngReader) == 0 {
		t.Fatal("empty PNG from io.Reader")
	}
}

func TestRenderPlannerPage_Errors(t *testing.T) {
	pdfBytes := buildMinimalPDF([]string{"Only One Page"})

	// Negative page index
	if _, err := render.RenderPlannerPage(pdfBytes, -1, nil, 100); !errors.Is(err, render.ErrPageOutOfBounds) {
		t.Errorf("expected ErrPageOutOfBounds for page -1, got: %v", err)
	}

	// Page index out of range
	if _, err := render.RenderPlannerPage(pdfBytes, 5, nil, 100); !errors.Is(err, render.ErrPageOutOfBounds) {
		t.Errorf("expected ErrPageOutOfBounds for page 5, got: %v", err)
	}

	// Unsupported document type
	if _, err := render.RenderPlannerPage(true, 0, nil, 100); !errors.Is(err, render.ErrInvalidDocument) {
		t.Errorf("expected ErrInvalidDocument for bool, got: %v", err)
	}

	// Nil fitz.Document
	var nilDoc *fitz.Document
	if _, err := render.RenderPlannerPage(nilDoc, 0, nil, 100); !errors.Is(err, render.ErrInvalidDocument) {
		t.Errorf("expected ErrInvalidDocument for nil *fitz.Document, got: %v", err)
	}

	// Non-existent file
	if _, err := render.RenderPlannerPage("/non/existent/file.pdf", 0, nil, 100); err == nil {
		t.Error("expected error for non-existent file, got nil")
	}

	// Corrupt PDF bytes
	if _, err := render.RenderPlannerPage([]byte("bad pdf"), 0, nil, 100); err == nil {
		t.Error("expected error for corrupt PDF bytes, got nil")
	}

	// Malformed rmBytes
	badRM := []byte("invalid-rm-data-not-a-valid-v6-format")
	if _, err := render.RenderPlannerPage(pdfBytes, 0, badRM, 100); err == nil {
		t.Error("expected error for malformed rmBytes, got nil")
	}
}
