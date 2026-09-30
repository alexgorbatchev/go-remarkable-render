package render_test

import (
	"bytes"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/go-remarkable-render"
)

type errReader struct{}

func (e *errReader) Read(p []byte) (n int, err error) {
	return 0, errors.New("reader failure")
}

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

	// 1. *render.Document
	doc, cleanup, err := render.OpenDocumentFromBytes(pdfBytes)
	if err != nil {
		t.Fatalf("render.OpenDocumentFromBytes failed: %v", err)
	}
	defer cleanup()

	pngDoc, err := render.RenderPlannerPage(doc, 0, rmBytes, 100)
	if err != nil {
		t.Fatalf("render from *render.Document failed: %v", err)
	}
	if len(pngDoc) == 0 {
		t.Fatal("empty PNG from *render.Document")
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

	// Test dpi <= 0 fallback
	pngDefaultDPI, err := render.RenderPlannerPage(pdfBytes, 0, nil, 0)
	if err != nil || len(pngDefaultDPI) == 0 {
		t.Fatalf("expected valid PNG with dpi <= 0: %v", err)
	}

	// OpenDocumentFromBytes invalid data
	_, _, errBadPDF := render.OpenDocumentFromBytes([]byte("not a pdf"))
	if errBadPDF == nil {
		t.Error("expected error for invalid PDF bytes")
	}

	// Test invalid rm bytes error
	_, errBadStrokes := render.RenderPlannerPage(pdfBytes, 0, []byte("invalid rm strokes"), 200)
	if errBadStrokes == nil {
		t.Error("expected error for invalid rm stroke bytes")
	}

	// Test invalid year in IndexPlannerDates
	if _, err := render.IndexPlannerDates(pdfBytes, 0); !errors.Is(err, render.ErrInvalidYear) {
		t.Errorf("expected ErrInvalidYear for year 0, got %v", err)
	}

	// Test invalid document type
	if _, err := render.IndexPlannerDates(12345, 2026); !errors.Is(err, render.ErrInvalidDocument) {
		t.Errorf("expected ErrInvalidDocument for integer input, got %v", err)
	}

	// Test doc.Text and doc.Links
	docObj, cleanupDoc, err := render.OpenDocumentFromBytes(pdfBytes)
	if err != nil {
		t.Fatalf("OpenDocumentFromBytes failed: %v", err)
	}
	defer cleanupDoc()

	docText, err := docObj.Text(0)
	if err != nil || len(docText) == 0 {
		t.Fatalf("doc.Text failed: %v", err)
	}
	_, _ = docObj.Text(-1)
	_, _ = docObj.Text(999)

	docLinks, err := docObj.Links(0)
	if err != nil {
		t.Fatalf("doc.Links failed: %v", err)
	}
	_ = docLinks
	_, _ = docObj.Links(-1)
	_, _ = docObj.Links(999)

	// Test Links with real linked document
	linkedBytes, err := os.ReadFile("testdata/linked_pages.pdf")
	if err == nil {
		linkedDoc, cleanupLinked, err := render.OpenDocumentFromBytes(linkedBytes)
		if err == nil {
			defer cleanupLinked()
			links, err := linkedDoc.Links(0)
			if err == nil && len(links) > 0 {
				_ = links[0].TargetPage
			}
		}
	}

	// Test out of bounds page index
	if _, err := render.RenderPlannerPage(pdfBytes, -1, nil, 200); !errors.Is(err, render.ErrPageOutOfBounds) {
		t.Errorf("expected ErrPageOutOfBounds for page -1, got %v", err)
	}
	if _, err := render.RenderPlannerPage(pdfBytes, 999, nil, 200); !errors.Is(err, render.ErrPageOutOfBounds) {
		t.Errorf("expected ErrPageOutOfBounds for page 999, got %v", err)
	}

	// Test failing reader
	if _, err := render.RenderPlannerPage(&errReader{}, 0, nil, 200); err == nil {
		t.Error("expected error for failing reader")
	}

	// Nil render.Document
	var nilDoc *render.Document
	if _, err := render.RenderPlannerPage(nilDoc, 0, nil, 100); !errors.Is(err, render.ErrInvalidDocument) {
		t.Errorf("expected ErrInvalidDocument for nil *render.Document, got: %v", err)
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
