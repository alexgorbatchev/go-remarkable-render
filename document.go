package render

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/references"
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

	errMissingResponse     = errors.New("pdfium returned no response")
	errMissingNextStartPos = errors.New("pdfium returned a link without a next start position")

	poolOnce   sync.Once
	pdfiumPool pdfium.Pool
	poolErr    error
)

// noTargetPage is the TargetPage of a link without an in-document destination.
const noTargetPage = -1

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

	textResp, err := pdfiumResponse(d.instance.GetPageText(&requests.GetPageText{
		Page: requests.Page{
			ByIndex: &requests.PageByIndex{
				Document: d.handle.Document,
				Index:    pageIdx,
			},
		},
	}))
	if err != nil {
		return "", fmt.Errorf("extracting text from page %d: %w", pageIdx, err)
	}
	return textResp.Text, nil
}

// Links extracts internal link targets and external hyperlinks from a page.
// A page without links yields an empty list and a nil error. Any failed
// PDFium request returns an error and no links.
func (d *Document) Links(pageIdx int) ([]PageLink, error) {
	if pageIdx < 0 || pageIdx >= d.pages {
		return nil, fmt.Errorf("%w: page index %d", ErrPageOutOfBounds, pageIdx)
	}

	var results []PageLink
	pos := 0

	for {
		linkResp, err := pdfiumResponse(d.instance.FPDFLink_Enumerate(&requests.FPDFLink_Enumerate{
			Page: requests.Page{
				ByIndex: &requests.PageByIndex{
					Document: d.handle.Document,
					Index:    pageIdx,
				},
			},
			StartPos: pos,
		}))
		if err != nil {
			return nil, fmt.Errorf("enumerating links on page %d: %w", pageIdx, err)
		}
		// go-pdfium signals the end of enumeration with an empty response:
		// PDFium's FPDFLink_Enumerate returned false, leaving Link nil.
		if linkResp.Link == nil {
			return results, nil
		}
		if linkResp.NextStartPos == nil {
			return nil, fmt.Errorf("enumerating links on page %d: %w", pageIdx, errMissingNextStartPos)
		}
		pos = *linkResp.NextStartPos

		targetPage, err := d.linkTargetPage(*linkResp.Link)
		if err != nil {
			return nil, fmt.Errorf("resolving link %d on page %d: %w", len(results), pageIdx, err)
		}

		results = append(results, PageLink{
			Index:      len(results),
			TargetPage: targetPage,
			URI:        fmt.Sprintf("#page=%d", targetPage+1),
		})
	}
}

// linkTargetPage resolves the page a link points to, or noTargetPage when it
// has no in-document destination. A direct destination takes precedence; the
// destination of the link's action is the fallback.
func (d *Document) linkTargetPage(link references.FPDF_LINK) (int, error) {
	destResp, err := pdfiumResponse(d.instance.FPDFLink_GetDest(&requests.FPDFLink_GetDest{
		Document: d.handle.Document,
		Link:     link,
	}))
	if err != nil {
		return 0, fmt.Errorf("getting link destination: %w", err)
	}
	if destResp.Dest != nil {
		targetPage, err := d.destPageIndex(*destResp.Dest)
		if err != nil || targetPage != noTargetPage {
			return targetPage, err
		}
	}

	actResp, err := pdfiumResponse(d.instance.FPDFLink_GetAction(&requests.FPDFLink_GetAction{
		Link: link,
	}))
	if err != nil {
		return 0, fmt.Errorf("getting link action: %w", err)
	}
	if actResp.Action == nil {
		return noTargetPage, nil
	}

	actDest, err := pdfiumResponse(d.instance.FPDFAction_GetDest(&requests.FPDFAction_GetDest{
		Document: d.handle.Document,
		Action:   *actResp.Action,
	}))
	if err != nil {
		return 0, fmt.Errorf("getting action destination: %w", err)
	}
	if actDest.Dest == nil {
		return noTargetPage, nil
	}
	return d.destPageIndex(*actDest.Dest)
}

// destPageIndex returns the page index of a destination. PDFium reports an
// unresolvable destination as noTargetPage.
func (d *Document) destPageIndex(dest references.FPDF_DEST) (int, error) {
	pageIndex, err := pdfiumResponse(d.instance.FPDFDest_GetDestPageIndex(&requests.FPDFDest_GetDestPageIndex{
		Document: d.handle.Document,
		Dest:     dest,
	}))
	if err != nil {
		return 0, fmt.Errorf("getting destination page index: %w", err)
	}
	return pageIndex.Index, nil
}

// pdfiumResponse rejects a go-pdfium call that returned neither a response
// nor an error, so a missing response is never mistaken for empty data.
func pdfiumResponse[T any](resp *T, err error) (*T, error) {
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, errMissingResponse
	}
	return resp, nil
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
