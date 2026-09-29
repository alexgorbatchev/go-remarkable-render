package render_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/go-remarkable-render"
	"github.com/gen2brain/go-fitz"
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

	doc, err := fitz.NewFromMemory(pdfBytes)
	if err != nil {
		t.Fatalf("fitz.NewFromMemory failed: %v", err)
	}
	defer doc.Close()

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

	// 2. Test indexing from *fitz.Document
	indexedFromDoc, err := render.IndexPlannerDates(doc, 2026)
	if err != nil {
		t.Fatalf("IndexPlannerDates from *fitz.Document failed: %v", err)
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

	// Nil fitz.Document
	var nilDoc *fitz.Document
	if _, err := render.IndexPlannerDates(nilDoc, 2026); !errors.Is(err, render.ErrInvalidDocument) {
		t.Errorf("expected ErrInvalidDocument for nil *fitz.Document, got: %v", err)
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
	realPDF := "/Users/agorbatchev/development/envoy.com/.workspace/.tmp/remarkable/0e40ea7e-2ee9-4f96-80cc-a7e11f28c53a-2026-09-28T21-05-50-128Z.pdf"
	if _, err := os.Stat(realPDF); os.IsNotExist(err) {
		t.Skip("skipping real planner PDF test: file not present")
	}

	indexed, err := render.IndexPlannerDates(realPDF, 2026)
	if err != nil {
		t.Fatalf("failed to index real PDF: %v", err)
	}

	if len(indexed) < 250 {
		t.Errorf("expected at least 250 indexed dates in real planner, got %d", len(indexed))
	}

	jan1 := indexed["2026-01-01"]
	if jan1 == nil || jan1["day"] != 1 || jan1["notes"] != 262 {
		t.Errorf("unexpected indexing for 2026-01-01: %v", jan1)
	}

	sep28 := indexed["2026-09-28"]
	if sep28 == nil || sep28["day"] != 193 || sep28["notes"] != 454 {
		t.Errorf("unexpected indexing for 2026-09-28: %v", sep28)
	}
}
