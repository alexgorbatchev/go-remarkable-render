package render

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/enums"
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
	errMissingURIPath      = errors.New("pdfium returned no URI for a URI action")

	poolOnce   sync.Once
	pdfiumPool pdfium.Pool
	poolErr    error
)

const (
	// noTargetPage is the TargetPage of a link without an in-document destination.
	noTargetPage = -1

	// instanceTimeout bounds the wait for a free instance in the PDFium pool.
	instanceTimeout = 30 * time.Second
)

// PageLink represents a resolved link annotation on a page.
type PageLink struct {
	// Index is the 0-based position of the link among the page's links.
	Index int

	// TargetPage is the 0-based page of this document the link leads to, or
	// -1 when it leads to no page of this document.
	TargetPage int

	// URI is "#page=N" (TargetPage+1) for a link to a page of this document.
	// For a URI action it is the URI PDFium reports: the raw bytes of the
	// /URI string, not validated as UTF-8. When the document catalog has a
	// /URI /Base and the /URI string contains no ':' or starts with one,
	// PDFium concatenates the base in front of it. That is plain string
	// concatenation, not RFC 3986 reference resolution, so it can produce a
	// malformed URL (base "https://a/b.html" and "c.html" yield
	// "https://a/b.htmlc.html"), and a URI with ':' after its first byte is
	// never prefixed. Without a /Base, a relative URI is returned as written.
	// URI is empty for a URI action whose /URI string is empty in a document
	// without a /Base, and for any other link, such as a remote go-to,
	// launch, or named action, or a link whose destination does not resolve.
	URI string
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

// Text extracts the plain text content of a page. A page without text yields
// an empty string and a nil error. A failed PDFium request, including one that
// returns no response, returns an error.
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

		target, err := d.resolveLink(*linkResp.Link)
		if err != nil {
			return nil, fmt.Errorf("resolving link %d on page %d: %w", len(results), pageIdx, err)
		}

		results = append(results, PageLink{
			Index:      len(results),
			TargetPage: target.page,
			URI:        target.uri,
		})
	}
}

// linkTarget is where a link leads: an in-document page with its "#page=N"
// fragment, an external URI with page noTargetPage, or neither.
type linkTarget struct {
	page int
	uri  string
}

// noLinkTarget is the target of a link that leads neither to a page of the
// document nor to a URI.
var noLinkTarget = linkTarget{page: noTargetPage}

// pageTarget returns the target for a resolved destination page index.
func pageTarget(page int) linkTarget {
	if page == noTargetPage {
		return noLinkTarget
	}
	return linkTarget{page: page, uri: fmt.Sprintf("#page=%d", page+1)}
}

// resolveLink determines where a link leads. ISO 32000-1 table 173 forbids a
// link with both /A and /Dest, so an action, when present, defines the target;
// otherwise the link's /Dest does. The action type is checked before any
// destination is read because PDFium also reports the destination of a remote
// or embedded go-to action, which addresses a page of another document.
func (d *Document) resolveLink(link references.FPDF_LINK) (linkTarget, error) {
	actResp, err := pdfiumResponse(d.instance.FPDFLink_GetAction(&requests.FPDFLink_GetAction{
		Link: link,
	}))
	if err != nil {
		return linkTarget{}, fmt.Errorf("getting link action: %w", err)
	}
	if actResp.Action != nil {
		return d.resolveAction(*actResp.Action)
	}

	destResp, err := pdfiumResponse(d.instance.FPDFLink_GetDest(&requests.FPDFLink_GetDest{
		Document: d.handle.Document,
		Link:     link,
	}))
	if err != nil {
		return linkTarget{}, fmt.Errorf("getting link destination: %w", err)
	}
	return d.destTarget(destResp.Dest)
}

// resolveAction determines where a link action leads. Only a go-to action
// addresses a page of this document and only a URI action carries a URI;
// every other action type has no target.
func (d *Document) resolveAction(action references.FPDF_ACTION) (linkTarget, error) {
	typeResp, err := pdfiumResponse(d.instance.FPDFAction_GetType(&requests.FPDFAction_GetType{
		Action: action,
	}))
	if err != nil {
		return linkTarget{}, fmt.Errorf("getting action type: %w", err)
	}

	switch typeResp.Type {
	case enums.FPDF_ACTION_ACTION_GOTO:
		destResp, err := pdfiumResponse(d.instance.FPDFAction_GetDest(&requests.FPDFAction_GetDest{
			Document: d.handle.Document,
			Action:   action,
		}))
		if err != nil {
			return linkTarget{}, fmt.Errorf("getting action destination: %w", err)
		}
		return d.destTarget(destResp.Dest)
	case enums.FPDF_ACTION_ACTION_URI:
		uriResp, err := pdfiumResponse(d.instance.FPDFAction_GetURIPath(&requests.FPDFAction_GetURIPath{
			Document: d.handle.Document,
			Action:   action,
		}))
		if err != nil {
			return linkTarget{}, fmt.Errorf("getting action URI: %w", err)
		}
		// PDFium counts the terminating NUL in the URI length, so a URI
		// action yields a URI even when its /URI string is empty; go-pdfium
		// returns none only when PDFium reports a zero length, its error.
		if uriResp.URIPath == nil {
			return linkTarget{}, fmt.Errorf("getting action URI: %w", errMissingURIPath)
		}
		return linkTarget{page: noTargetPage, uri: *uriResp.URIPath}, nil
	default:
		return noLinkTarget, nil
	}
}

// destTarget returns the target of an optional destination.
func (d *Document) destTarget(dest *references.FPDF_DEST) (linkTarget, error) {
	if dest == nil {
		return noLinkTarget, nil
	}
	page, err := d.destPageIndex(*dest)
	if err != nil {
		return linkTarget{}, err
	}
	return pageTarget(page), nil
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

// OpenDocumentFromBytes opens a PDF document from raw byte slice using PDFium
// WebAssembly. Call the returned cleanup function to release the document. A
// failure to open the document or to read its page count returns an error and
// releases everything acquired.
func OpenDocumentFromBytes(data []byte) (*Document, func(), error) {
	pool, err := getPool()
	if err != nil {
		return nil, nil, fmt.Errorf("initializing pdfium pool: %w", err)
	}

	instance, err := pool.GetInstance(instanceTimeout)
	if err != nil {
		return nil, nil, fmt.Errorf("acquiring pdfium instance: %w", err)
	}
	return openDocument(instance, data)
}

// openDocument opens data on instance and takes ownership of instance: it is
// closed when opening fails, or by the returned cleanup function.
func openDocument(instance pdfium.Pdfium, data []byte) (*Document, func(), error) {
	doc, err := pdfiumResponse(instance.OpenDocument(&requests.OpenDocument{
		File: &data,
	}))
	if err != nil {
		_ = instance.Close() // best-effort release; the open error is what the caller needs
		return nil, nil, fmt.Errorf("opening PDF document: %w", err)
	}

	cleanup := func() {
		_, _ = instance.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document})
		_ = instance.Close()
	}

	countResp, err := pdfiumResponse(instance.FPDF_GetPageCount(&requests.FPDF_GetPageCount{
		Document: doc.Document,
	}))
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("counting PDF pages: %w", err)
	}

	d := &Document{
		instance: instance,
		handle:   doc,
		pages:    countResp.PageCount,
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
