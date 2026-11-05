package render

import (
	"bytes"
	"image/png"
	"testing"
)

const testSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="120" height="80"><path d="M5 5L110 70" stroke="black" stroke-width="4"/><rect x="20" y="10" width="60" height="30" fill="yellow" opacity="0.4"/></svg>`

func TestRasterizeSVG_IsolatedRenders(t *testing.T) {
	const width, height = 180, 120
	want, err := rasterizeSVG([]byte(testSVG), width, height)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(want))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != width || img.Bounds().Dy() != height {
		t.Fatalf("bounds = %v", img.Bounds())
	}
	if _, _, _, alpha := img.At(0, 0).RGBA(); alpha != 0 {
		t.Fatal("SVG background must be transparent")
	}
	if _, err := rasterizeSVG([]byte("invalid SVG"), width, height); err == nil {
		t.Fatal("invalid SVG succeeded")
	}
	for range 3 {
		got, err := rasterizeSVG([]byte(testSVG), width, height)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("sequential render differs: %v", err)
		}
	}
	for range 4 {
		t.Run("concurrent", func(t *testing.T) {
			t.Parallel()
			got, err := rasterizeSVG([]byte(testSVG), width, height)
			if err != nil || !bytes.Equal(got, want) {
				t.Errorf("concurrent render differs: %v", err)
			}
		})
	}
}

func BenchmarkRasterizeSVG(b *testing.B) {
	// Exclude the first compilation so this measures repeated rendering.
	if _, err := rasterizeSVG([]byte(testSVG), 180, 120); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		if _, err := rasterizeSVG([]byte(testSVG), 180, 120); err != nil {
			b.Fatal(err)
		}
	}
}
