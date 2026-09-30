package render

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/responses"
	"github.com/klippa-app/go-pdfium/webassembly"
)

var (
	// ErrInvalidDocument indicates an unsupported document parameter type or nil document.
	ErrInvalidDocument = errors.New("unsupported pdfDocOrPath: expected *Document, string (file path), []byte, or io.Reader")

	// ErrInvalidYear indicates a non-positive year was provided.
	ErrInvalidYear = errors.New("year must be positive")

	// ErrPageOutOfBounds indicates a requested page index exceeds document bounds.
	ErrPageOutOfBounds = errors.New("page index out of bounds")

	poolOnce   sync.Once
	pdfiumPool pdfium.Pool
	poolErr    error
)

// Document wraps an open PDFium document instance.
type Document struct {
	instance pdfium.Pdfium
	handle   *responses.OpenDocument
	pages    int
}

// NumPage returns the total page count of the document.
func (d *Document) NumPage() int {
	return d.pages
}

func getPool() (pdfium.Pool, error) {
	poolOnce.Do(func() {
		pdfiumPool, poolErr = webassembly.Init(webassembly.Config{
			MinIdle:  1,
			MaxIdle:  2,
			MaxTotal: 4,
		})
	})
	return pdfiumPool, poolErr
}

// OpenDocumentFromBytes opens a PDF document from raw byte slice using PDFium WebAssembly.
func OpenDocumentFromBytes(data []byte) (*Document, func(), error) {
	pool, err := getPool()
	if err != nil {
		return nil, nil, fmt.Errorf("initializing pdfium pool: %w", err)
	}

	instance, err := pool.GetInstance(30 * time.Second)
	if err != nil {
		return nil, nil, fmt.Errorf("acquiring pdfium instance: %w", err)
	}

	doc, err := instance.OpenDocument(&requests.OpenDocument{
		File: &data,
	})
	if err != nil {
		_ = instance.Close()
		return nil, nil, fmt.Errorf("opening PDF document: %w", err)
	}

	countResp, _ := instance.FPDF_GetPageCount(&requests.FPDF_GetPageCount{
		Document: doc.Document,
	})
	pageCount := 0
	if countResp != nil {
		pageCount = countResp.PageCount
	}

	d := &Document{
		instance: instance,
		handle:   doc,
		pages:    pageCount,
	}

	cleanup := func() {
		_, _ = instance.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document})
		_ = instance.Close()
	}

	return d, cleanup, nil
}

// resolveDocument resolves any accepted document representation (*Document,
// string path, []byte, or io.Reader) into an active *Document.
func resolveDocument(pdfDocOrPath any) (*Document, func(), error) {
	switch v := pdfDocOrPath.(type) {
	case *Document:
		if v == nil {
			return nil, nil, fmt.Errorf("%w: *Document cannot be nil", ErrInvalidDocument)
		}
		return v, func() {}, nil
	case string:
		data, err := os.ReadFile(v)
		if err != nil {
			return nil, nil, fmt.Errorf("reading PDF file %q: %w", v, err)
		}
		return OpenDocumentFromBytes(data)
	case []byte:
		return OpenDocumentFromBytes(v)
	case io.Reader:
		data, err := io.ReadAll(v)
		if err != nil {
			return nil, nil, fmt.Errorf("reading PDF from reader: %w", err)
		}
		return OpenDocumentFromBytes(data)
	default:
		return nil, nil, fmt.Errorf("%w: received %T", ErrInvalidDocument, pdfDocOrPath)
	}
}
