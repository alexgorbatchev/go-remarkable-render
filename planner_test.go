package render_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alexgorbatchev/go-remarkable-render"
)

func TestIndexPlannerDates_Success(t *testing.T) {
	pdfBytes := buildMinimalPDF(
		// Page 0: Cover / Overview
		[]string{"2026 Planner Year at a Glance"},
		// Page 1: Jan 1 Day
		[]string{"Jan 1 Thursday New Year's Day Notes"},
		// Page 2: Jan 1 Notes
		[]string{"Jan 1 Thursday Day"},
		// Page 3: Jan 15 Day
		[]string{"Jan 15 Wednesday Notes"},
		// Page 4: Jan 15 Notes
		[]string{"Jan 15 Wednesday Day"},
		// Page 5: Feb 28 Day
		[]string{"Feb 28 Saturday Notes"},
		// Page 6: Feb 28 Notes
		[]string{"Feb 28 Saturday Day"},
		// Page 7: Dec 31 Day
		[]string{"Dec 31 Thursday Notes"},
		// Page 8: Dec 31 Notes
		[]string{"Dec 31 Thursday Day"},
		// Page 9: Empty page
		[]string{},
		// Page 10: Too few tokens
		[]string{"Jan 1"},
		// Page 11: Invalid day number string
		[]string{"Jan foo Thursday Notes"},
		// Page 12: Day number out of bounds
		[]string{"Jan 99 Thursday Notes"},
		// Page 13: Missing weekday
		[]string{"Jan 15 Notes"},
		// Page 14: Has weekday but neither Notes nor Day
		[]string{"Jan 15 Wednesday Overview"},
	)

	doc, cleanup, err := render.OpenDocumentFromBytes(pdfBytes)
	if err != nil {
		t.Fatalf("render.OpenDocumentFromBytes failed: %v", err)
	}
	defer cleanup()

	// 1. Test indexing from []byte
	indexed, err := render.IndexPlannerDates(pdfBytes, 2026)
	if err != nil {
		t.Fatalf("IndexPlannerDates failed: %v", err)
	}

	expected := map[string]map[string]int{
		"2026-01-01": {"day": 1, "notes": 2},
		"2026-01-15": {"day": 3, "notes": 4},
		"2026-02-28": {"day": 5, "notes": 6},
		"2026-03-31": nil, // Should not exist
		"2026-12-31": {"day": 7, "notes": 8},
	}

	for dateStr, expectedPages := range expected {
		if expectedPages == nil {
			if _, exists := indexed[dateStr]; exists {
				t.Errorf("date %s unexpectedly found in indexed map", dateStr)
			}
			continue
		}
		pages, ok := indexed[dateStr]
		if !ok {
			t.Errorf("expected date %s not found in indexed map", dateStr)
			continue
		}
		if pages["day"] != expectedPages["day"] {
			t.Errorf("date %s: expected day page %d, got %d", dateStr, expectedPages["day"], pages["day"])
		}
		if pages["notes"] != expectedPages["notes"] {
			t.Errorf("date %s: expected notes page %d, got %d", dateStr, expectedPages["notes"], pages["notes"])
		}
	}

	// 2. Test indexing from *render.Document
	indexedFromDoc, err := render.IndexPlannerDates(doc, 2026)
	if err != nil {
		t.Fatalf("IndexPlannerDates from *render.Document failed: %v", err)
	}
	if len(indexedFromDoc) != len(indexed) {
		t.Errorf("expected %d indexed dates from doc, got %d", len(indexed), len(indexedFromDoc))
	}

	// 3. Test indexing from file path
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "planner.pdf")
	if err := os.WriteFile(filePath, pdfBytes, 0644); err != nil {
		t.Fatalf("failed to write temp PDF: %v", err)
	}

	indexedFromFile, err := render.IndexPlannerDates(filePath, 2026)
	if err != nil {
		t.Fatalf("IndexPlannerDates from file path failed: %v", err)
	}
	if len(indexedFromFile) != len(indexed) {
		t.Errorf("expected %d indexed dates from file, got %d", len(indexed), len(indexedFromFile))
	}

	// 4. Test indexing from io.Reader
	indexedFromReader, err := render.IndexPlannerDates(bytes.NewReader(pdfBytes), 2026)
	if err != nil {
		t.Fatalf("IndexPlannerDates from io.Reader failed: %v", err)
	}
	if len(indexedFromReader) != len(indexed) {
		t.Errorf("expected %d indexed dates from reader, got %d", len(indexed), len(indexedFromReader))
	}
}

func TestIndexPlannerDates_Errors(t *testing.T) {
	pdfBytes := buildMinimalPDF([]string{"Jan 1 Thursday Notes"})

	// Negative year
	if _, err := render.IndexPlannerDates(pdfBytes, -1); !errors.Is(err, render.ErrInvalidYear) {
		t.Errorf("expected ErrInvalidYear for -1, got: %v", err)
	}

	// Zero year
	if _, err := render.IndexPlannerDates(pdfBytes, 0); !errors.Is(err, render.ErrInvalidYear) {
		t.Errorf("expected ErrInvalidYear for 0, got: %v", err)
	}

	// Nil render.Document
	var nilDoc *render.Document
	if _, err := render.IndexPlannerDates(nilDoc, 2026); !errors.Is(err, render.ErrInvalidDocument) {
		t.Errorf("expected ErrInvalidDocument for nil *render.Document, got: %v", err)
	}

	// Unsupported type
	if _, err := render.IndexPlannerDates(12345, 2026); !errors.Is(err, render.ErrInvalidDocument) {
		t.Errorf("expected ErrInvalidDocument for integer, got: %v", err)
	}

	// Non-existent file path
	if _, err := render.IndexPlannerDates("/non/existent/path.pdf", 2026); err == nil {
		t.Error("expected error for non-existent file path, got nil")
	}

	// Corrupt PDF bytes
	if _, err := render.IndexPlannerDates([]byte("not a pdf"), 2026); err == nil {
		t.Error("expected error for corrupt PDF data, got nil")
	}
}

func TestIndexPlannerDates_RealPDF(t *testing.T) {
	realPDF := "testdata/oct1_notes_template.pdf"
	data, err := os.ReadFile(realPDF)
	if err != nil {
		t.Fatal(err)
	}
	doc, cleanup, err := render.OpenDocumentFromBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	text, err := doc.Text(0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(text, "Oct 1 Fri 2") {
		t.Fatalf("unexpected fixture header: %q", text)
	}
	indexed, err := render.IndexPlannerDates(realPDF, 2026)
	if err != nil {
		t.Fatalf("failed to index real PDF: %v", err)
	}

	want := map[string]map[string]int{"2026-10-01": {"notes": 0}}
	if !reflect.DeepEqual(indexed, want) {
		t.Errorf("index = %v, want %v", indexed, want)
	}
}

func TestIndexPlannerDates_HeaderValidity(t *testing.T) {
	tests := []struct {
		name  string
		year  int
		lines []string
		date  string
	}{
		{"leap day", 2024, []string{"Feb 29 Thursday Notes"}, "2024-02-29"},
		{"non leap day", 2026, []string{"Feb 29 Sunday Notes"}, ""},
		{"impossible February", 2026, []string{"Feb 31 Tuesday Notes"}, ""},
		{"impossible April", 2026, []string{"Apr 31 Thursday Notes"}, ""},
		{"body marker and weekday", 2026, []string{"Apr 5", "Meeting agenda", "Day trip on Sunday"}, ""},
		{"body weekday", 2026, []string{"Jun 7 Notes", "Weekly review", "Sunday"}, ""},
		{"body marker", 2026, []string{"Jun 7 Sunday", "Notes"}, ""},
		{"holiday name", 2026, []string{"Jan 1 Thursday New Year's Day Notes"}, "2026-01-01"},
		{"navigation header", 2026, []string{"Oct 1 Fri 2 Mon 5", "Thursday Notes 2026"}, "2026-10-01"},
		{"invalid navigation day", 2026, []string{"Oct 1 Fri 99", "Thursday Notes"}, ""},
		{"invalid navigation number", 2026, []string{"Oct 1 Fri two", "Thursday Notes"}, ""},
		{"navigation with body text", 2026, []string{"Oct 1 Fri 2", "Meeting", "Thursday Notes"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := render.IndexPlannerDates(buildMinimalPDF(tt.lines), tt.year)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]map[string]int{}
			if tt.date != "" {
				want[tt.date] = map[string]int{"day": 0}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("index = %v, want %v", got, want)
			}
		})
	}
}

func TestIndexPlannerDates_DuplicateViews(t *testing.T) {
	for _, marker := range []string{"Notes", "Day"} {
		t.Run(marker, func(t *testing.T) {
			header := []string{"Mar 3 Tuesday " + marker}
			got, err := render.IndexPlannerDates(buildMinimalPDF([]string{"Overview"}, header, header), 2026)
			if got != nil || err == nil || !strings.Contains(err.Error(), "2026-03-03") || !strings.Contains(err.Error(), "pages 1 and 2") {
				t.Fatalf("index = %v, error = %v; want nil index and collision naming both pages and date", got, err)
			}
		})
	}
}
