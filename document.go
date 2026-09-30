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

// PageLink represents a resolved link target on a page.
type PageLink struct {
	Index      int
	TargetPage int
	URI        string
}

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

// Text extracts the plain text content of a page.
func (d *Document) Text(pageIdx int) (string, error) {
	if pageIdx < 0 || pageIdx >= d.pages {
		return "", fmt.Errorf("%w: page index %d", ErrPageOutOfBounds, pageIdx)
	}

	textResp, err := d.instance.GetPageText(&requests.GetPageText{
		Page: requests.Page{
			ByIndex: &requests.PageByIndex{
				Document: d.handle.Document,
				Index:    pageIdx,
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("extracting text from page %d: %w", pageIdx, err)
	}
	if textResp == nil {
		return "", nil
	}
	return textResp.Text, nil
}

// Links extracts internal link targets and external hyperlinks from a page.
func (d *Document) Links(pageIdx int) ([]PageLink, error) {
	if pageIdx < 0 || pageIdx >= d.pages {
		return nil, fmt.Errorf("%w: page index %d", ErrPageOutOfBounds, pageIdx)
	}

	var results []PageLink
	pos := 0

	for {
		linkResp, err := d.instance.FPDFLink_Enumerate(&requests.FPDFLink_Enumerate{
			Page: requests.Page{
				ByIndex: &requests.PageByIndex{
					Document: d.handle.Document,
					Index:    pageIdx,
				},
			},
			StartPos: pos,
		})
		if err != nil || linkResp == nil || linkResp.Link == nil {
			break
		}
		if linkResp.NextStartPos != nil {
			pos = *linkResp.NextStartPos
		} else {
			break
		}

		targetPage := -1

		// 1. Direct Dest
		destResp, err := d.instance.FPDFLink_GetDest(&requests.FPDFLink_GetDest{
			Document: d.handle.Document,
			Link:     *linkResp.Link,
		})
		if err == nil && destResp != nil && destResp.Dest != nil {
			pageIndex, err := d.instance.FPDFDest_GetDestPageIndex(&requests.FPDFDest_GetDestPageIndex{
				Document: d.handle.Document,
				Dest:     *destResp.Dest,
			})
			if err == nil && pageIndex != nil {
				targetPage = pageIndex.Index
			}
		}

		// 2. Action Dest
		if targetPage == -1 {
			actResp, err := d.instance.FPDFLink_GetAction(&requests.FPDFLink_GetAction{
				Link: *linkResp.Link,
			})
			if err == nil && actResp != nil && actResp.Action != nil {
				actDest, err := d.instance.FPDFAction_GetDest(&requests.FPDFAction_GetDest{
					Document: d.handle.Document,
					Action:   *actResp.Action,
				})
				if err == nil && actDest != nil && actDest.Dest != nil {
					pageIndex, err := d.instance.FPDFDest_GetDestPageIndex(&requests.FPDFDest_GetDestPageIndex{
						Document: d.handle.Document,
						Dest:     *actDest.Dest,
					})
					if err == nil && pageIndex != nil {
						targetPage = pageIndex.Index
					}
				}
			}
		}

		results = append(results, PageLink{
			Index:      len(results),
			TargetPage: targetPage,
			URI:        fmt.Sprintf("#page=%d", targetPage+1),
		})
	}

	return results, nil
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
