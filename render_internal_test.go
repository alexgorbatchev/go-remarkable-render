package render

import (
	"bytes"
	"errors"
	"image/png"
	"os"
	"testing"

	"github.com/alexgorbatchev/go-rmscene"
	"github.com/klippa-app/go-pdfium/webassembly"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
)

const (
	// memoryLimitPages caps the guest memory of the PDFium instance in
	// TestRenderPlannerPage_ReusedDocumentReleasesBitmaps. 512 pages of 64 KiB
	// is 32 MiB: enough to initialize PDFium, open the planner fixture and
	// render one page at leakTestDPI, a bitmap of about 8 MiB, but not enough
	// to hold four such bitmaps at once.
	memoryLimitPages = 512

	// leakTestDPI is the resolution of the repeated renders.
	leakTestDPI = DefaultDPI

	// leakTestRenders is the number of renders through one *Document. It is
	// over twice the number of renders that leak enough bitmaps to exhaust
	// that memory.
	leakTestRenders = 10

	// releaseTestDPI is the resolution of the renders in
	// TestRenderPlannerPage_ReleasesRenderedPage, low to keep the stroke
	// rasterization and the pixel checks fast.
	releaseTestDPI = 72
)

// openMemoryLimitedDocument opens data on an instance of a new PDFium pool
// whose guest memory is capped at memoryLimitPages. The pool runs PDFium in
// wazero's interpreter, which skips the compilation of the module that the
// compiler would spend most of this test's time on. The document and the pool
// are released when the test ends.
func openMemoryLimitedDocument(t *testing.T, data []byte) *Document {
	t.Helper()
	features := api.CoreFeaturesV2 | experimental.CoreFeaturesExceptionHandling
	pool, err := webassembly.Init(webassembly.Config{
		MinIdle:       1,
		MaxIdle:       1,
		MaxTotal:      1,
		RuntimeConfig: wazero.NewRuntimeConfigInterpreter().WithCoreFeatures(features).WithMemoryLimitPages(memoryLimitPages),
	})
	if err != nil {
		t.Fatalf("initializing memory-limited pdfium pool: %v", err)
	}
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Errorf("closing memory-limited pdfium pool: %v", err)
		}
	})

	instance, err := pool.GetInstance(instanceTimeout)
	if err != nil {
		t.Fatalf("acquiring memory-limited pdfium instance: %v", err)
	}
	doc, cleanup, err := openDocument(instance, data)
	if err != nil {
		t.Fatalf("openDocument: %v", err)
	}
	t.Cleanup(cleanup)
	return doc
}

// TestRenderPlannerPage_ReusedDocumentReleasesBitmaps renders the same page
// many times through one *Document on an instance with capped memory. Each
// render allocates a page bitmap in the instance's memory, so a render that
// does not release its bitmap makes a later one fail to allocate.
func TestRenderPlannerPage_ReusedDocumentReleasesBitmaps(t *testing.T) {
	doc := openMemoryLimitedDocument(t, buildPlannerPDF(plannerHeaders[0]))

	for i := range leakTestRenders {
		if _, err := RenderPlannerPage(doc, 0, nil, leakTestDPI); err != nil {
			t.Fatalf("render %d of %d through one *Document: %v", i+1, leakTestRenders, err)
		}
	}
}

// TestRenderPlannerPage_ReleasesRenderedPage checks that every rendered page
// is released exactly once on every return path, and only after its image was
// last read. faultInjectingPdfium clears the pixels when the page is released,
// and the planner fixture page has no transparency, so PDFium renders it
// opaque: a transparent pixel in the result means the image was read after
// its release.
func TestRenderPlannerPage_ReleasesRenderedPage(t *testing.T) {
	strokes, err := os.ReadFile("testdata/oct1_notes_strokes.rm")
	if err != nil {
		t.Fatalf("reading stroke fixture: %v", err)
	}

	tests := []struct {
		name        string
		rmBytes     []byte
		fault       fault
		wantErr     error
		wantRenders int
	}{
		{name: "background only", wantRenders: 1},
		{name: "composited strokes", rmBytes: strokes, wantRenders: 1},
		{name: "malformed strokes", rmBytes: []byte("invalid rm strokes"), wantErr: rmscene.ErrInvalidHeader, wantRenders: 1},
		{name: "render error", fault: faultError, wantErr: errInjected},
		{name: "nil render response", fault: faultNilResponse, wantErr: errMissingResponse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, cleanup := openPlannerPDF(t)
			defer cleanup()
			f := &faultInjectingPdfium{Pdfium: doc.instance, method: "RenderPageInDPI", fault: tt.fault}
			doc.instance = f

			pngBytes, err := RenderPlannerPage(doc, 0, tt.rmBytes, releaseTestDPI)
			if f.renders != tt.wantRenders || f.renderCleanups != tt.wantRenders {
				t.Fatalf("RenderPlannerPage rendered %d pages and released %d, want %d rendered and each released once", f.renders, f.renderCleanups, tt.wantRenders)
			}
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("RenderPlannerPage error = %v, want error wrapping %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("RenderPlannerPage error = %v, want nil", err)
			}
			assertOpaquePNG(t, pngBytes)
		})
	}
}

// assertOpaquePNG fails the test unless pngBytes decodes to an image whose
// every pixel is fully opaque.
func assertOpaquePNG(t *testing.T, pngBytes []byte) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("decoding rendered PNG: %v", err)
	}
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a != 0xffff {
				t.Fatalf("rendered pixel (%d, %d) has alpha %#x, want opaque: the page image was read after its release", x, y, a)
			}
		}
	}
}
