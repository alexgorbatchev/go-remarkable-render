package render_test

import (
	"bytes"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/go-remarkable-render"
	"github.com/corona10/goimagehash"
)

func TestE2E_RenderGoldenComparison(t *testing.T) {
	testdataDir := "testdata"
	pdfPath := filepath.Join(testdataDir, "oct1_notes_template.pdf")
	rmPath := filepath.Join(testdataDir, "oct1_notes_strokes.rm")
	goldenPath := filepath.Join(testdataDir, "oct1_notes_golden.png")

	if _, err := os.Stat(pdfPath); err != nil {
		t.Skipf("skipping E2E test, fixture not found: %v", err)
	}

	rmBytes, err := os.ReadFile(rmPath)
	if err != nil {
		t.Fatalf("failed to read .rm fixture: %v", err)
	}

	goldenBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("failed to read golden PNG fixture: %v", err)
	}

	goldenImg, err := png.Decode(bytes.NewReader(goldenBytes))
	if err != nil {
		t.Fatalf("failed to decode golden PNG: %v", err)
	}

	// Render page with Go library at 200 DPI
	renderedPNG, err := render.RenderPlannerPage(pdfPath, 0, rmBytes, 200)
	if err != nil {
		t.Fatalf("RenderPlannerPage failed: %v", err)
	}

	renderedImg, err := png.Decode(bytes.NewReader(renderedPNG))
	if err != nil {
		t.Fatalf("failed to decode rendered PNG: %v", err)
	}

	// 1. Dimensions check
	gb := goldenImg.Bounds()
	rb := renderedImg.Bounds()
	if gb.Dx() != rb.Dx() || gb.Dy() != rb.Dy() {
		t.Fatalf("dimension mismatch: golden=%dx%d, rendered=%dx%d", gb.Dx(), gb.Dy(), rb.Dx(), rb.Dy())
	}

	// 2. Perceptual image hash comparison (goimagehash)
	goldenHash, err := goimagehash.DifferenceHash(goldenImg)
	if err != nil {
		t.Fatalf("compute golden hash: %v", err)
	}

	renderedHash, err := goimagehash.DifferenceHash(renderedImg)
	if err != nil {
		t.Fatalf("compute rendered hash: %v", err)
	}

	dist, err := goldenHash.Distance(renderedHash)
	if err != nil {
		t.Fatalf("compute hash distance: %v", err)
	}

	t.Logf("Perceptual DifferenceHash distance: %d (golden vs rendered)", dist)
	if dist > 2 {
		t.Errorf("perceptual distance %d exceeds tolerance 2", dist)
	}

	// 3. Pixel-level channel mean absolute error
	var totalDelta float64
	totalPixels := float64(gb.Dx() * gb.Dy())

	for y := 0; y < gb.Dy(); y += 2 {
		for x := 0; x < gb.Dx(); x += 2 {
			gr, gg, gb, _ := goldenImg.At(x, y).RGBA()
			rr, rg, rb, _ := renderedImg.At(x, y).RGBA()

			dr := math.Abs(float64(gr>>8) - float64(rr>>8))
			dg := math.Abs(float64(gg>>8) - float64(rg>>8))
			db := math.Abs(float64(gb>>8) - float64(rb>>8))

			totalDelta += (dr + dg + db) / 3.0
		}
	}

	sampledPixels := totalPixels / 4.0
	meanDelta := totalDelta / sampledPixels
	t.Logf("Mean pixel channel difference across image: %.3f / 255.0", meanDelta)

	if meanDelta > 8.0 {
		t.Errorf("mean pixel difference %.3f exceeds threshold 8.0", meanDelta)
	}
}
