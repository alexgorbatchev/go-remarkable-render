package render

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const (
	plannerYear = 2026

	// failingPlannerPage is the page whose text extraction fails. Page 0
	// indexes 2026-01-01 before it, so a partial index would not be empty.
	failingPlannerPage = 1
)

// plannerHeaders are the page texts of the planner fixture, one per page.
var plannerHeaders = []string{
	"Jan 1 Thursday Notes",
	"Jan 1 Thursday Day",
	"Jan 2 Friday Notes",
}

// buildPlannerPDF builds a PDF with one page per header, each drawing its
// header in 12pt Helvetica.
func buildPlannerPDF(headers ...string) []byte {
	kids := make([]string, len(headers))
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"", // the page tree, filled in once the page object numbers are known
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	for i, header := range headers {
		pageObj := len(objects) + 1
		kids[i] = fmt.Sprintf("%d 0 R", pageObj)
		content := fmt.Sprintf("BT /F1 12 Tf 72 500 Td (%s) Tj ET\n", header)
		objects = append(objects,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 447.874 595.275] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", pageObj+1),
			fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content),
		)
	}
	objects[1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(headers))
	return encodePDF(objects)
}

func openPlannerPDF(t *testing.T) (*Document, func()) {
	t.Helper()
	doc, cleanup, err := OpenDocumentFromBytes(buildPlannerPDF(plannerHeaders...))
	if err != nil {
		t.Fatalf("OpenDocumentFromBytes: %v", err)
	}
	return doc, cleanup
}

func TestIndexPlannerDates_PageTextFailures(t *testing.T) {
	t.Run("fixture indexes every page", func(t *testing.T) {
		doc, cleanup := openPlannerPDF(t)
		defer cleanup()

		got, err := IndexPlannerDates(doc, plannerYear)
		if err != nil {
			t.Fatalf("IndexPlannerDates error = %v, want nil", err)
		}
		want := map[string]map[string]int{
			"2026-01-01": {"day": 0, "notes": 1},
			"2026-01-02": {"day": 2},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("IndexPlannerDates = %v, want %v", got, want)
		}
	})

	tests := []struct {
		fault fault
		want  error
	}{
		{faultError, errInjected},
		{faultNilResponse, errMissingResponse},
	}
	for _, tt := range tests {
		t.Run(tt.fault.String(), func(t *testing.T) {
			doc, cleanup := openPlannerPDF(t)
			defer cleanup()
			doc.instance = &faultInjectingPdfium{
				Pdfium:    doc.instance,
				method:    "GetPageText",
				fault:     tt.fault,
				faultPage: new(failingPlannerPage),
			}

			indexed, err := IndexPlannerDates(doc, plannerYear)
			if !errors.Is(err, tt.want) {
				t.Fatalf("IndexPlannerDates with failing GetPageText on page %d = %v, error %v; want error wrapping %v", failingPlannerPage, indexed, err, tt.want)
			}
			if indexed != nil {
				t.Fatalf("IndexPlannerDates with failing GetPageText on page %d returned partial index %v", failingPlannerPage, indexed)
			}
			if !strings.Contains(err.Error(), fmt.Sprintf("page %d", failingPlannerPage)) {
				t.Fatalf("IndexPlannerDates with failing GetPageText error = %q, want page %d context", err, failingPlannerPage)
			}
		})
	}
}

func TestIndexPlannerDates_ReleasedDocumentFails(t *testing.T) {
	doc, cleanup := openPlannerPDF(t)
	cleanup()

	indexed, err := IndexPlannerDates(doc, plannerYear)
	if err == nil {
		t.Fatalf("IndexPlannerDates on released document = %v, nil error; want error", indexed)
	}
	if indexed != nil {
		t.Fatalf("IndexPlannerDates on released document returned partial index %v", indexed)
	}
	if !strings.Contains(err.Error(), "page 0") || !strings.Contains(err.Error(), "instance is closed") {
		t.Fatalf("IndexPlannerDates on released document error = %q, want page 0 context and PDFium closed-instance cause", err)
	}
}
