package render

import (
	"errors"
	"fmt"
	"io"

	"github.com/gen2brain/go-fitz"
)

var (
	// ErrInvalidDocument indicates an unsupported document parameter type or nil document.
	ErrInvalidDocument = errors.New("unsupported pdfDocOrPath: expected *fitz.Document, string (file path), []byte, or io.Reader")

	// ErrInvalidYear indicates a non-positive year was provided.
	ErrInvalidYear = errors.New("year must be positive")

	// ErrPageOutOfBounds indicates a requested page index exceeds document bounds.
	ErrPageOutOfBounds = errors.New("page index out of bounds")
)

// resolveDocument resolves any accepted document representation (*fitz.Document,
// string path, []byte, or io.Reader) into an active *fitz.Document.
// It returns a cleanup function that closes the document only if it was opened locally.
func resolveDocument(pdfDocOrPath any) (*fitz.Document, func(), error) {
	switch v := pdfDocOrPath.(type) {
	case *fitz.Document:
		if v == nil {
			return nil, nil, fmt.Errorf("%w: *fitz.Document cannot be nil", ErrInvalidDocument)
		}
		return v, func() {}, nil
	case string:
		doc, err := fitz.New(v)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to open PDF file %q: %w", v, err)
		}
		return doc, func() { _ = doc.Close() }, nil
	case []byte:
		doc, err := fitz.NewFromMemory(v)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to open PDF from memory: %w", err)
		}
		return doc, func() { _ = doc.Close() }, nil
	case io.Reader:
		doc, err := fitz.NewFromReader(v)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to open PDF from reader: %w", err)
		}
		return doc, func() { _ = doc.Close() }, nil
	default:
		return nil, nil, fmt.Errorf("%w: received %T", ErrInvalidDocument, pdfDocOrPath)
	}
}
