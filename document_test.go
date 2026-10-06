package render

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/responses"
)

const (
	linkSourcePage = 0
	linkTargetPage = 1
	actionLinkPage = 2
)

// URIs of the action links on actionLinkPage, as the PDF strings encode them
// and as Links must report them. The absolute URI carries the UTF-8 bytes of
// "é" as octal escapes; the invalid-UTF-8 URI carries the byte 0xFF. PDFium
// concatenates the catalog's /URI /Base in front of a URI that contains no
// ':' or starts with one; it does not resolve the reference per RFC 3986.
// uriBase deliberately does not end in '/' so the concatenation is visible.
const (
	absoluteURI       = "https://example.com/path?q=1&r=café#frag"
	relativeURI       = "guide.html"
	colonRelativeURI  = "guide.html?t=10:30"
	leadingColonURI   = ":note"
	invalidUTF8URI    = "https://example.com/\xff"
	uriBase           = "https://example.com/docs/index.html"
	absoluteURIPDF    = "https://example.com/path?q=1&r=caf\\303\\251#frag"
	invalidUTF8URIPDF = "https://example.com/\\377"
)

// buildLinkPDF builds a three-page PDF whose catalog carries /URI /Base base,
// or no /URI dictionary when base is empty. Page 0 carries two link
// annotations to page 1: one with a direct /Dest and one with a /GoTo action.
// Page 1 has no annotations. Page 2 carries the links actionPageLinks
// describes, none of which has an in-document destination.
func buildLinkPDF(base string) []byte {
	catalog := "<< /Type /Catalog /Pages 2 0 R >>"
	if base != "" {
		catalog = "<< /Type /Catalog /Pages 2 0 R /URI << /Base (" + base + ") >> >>"
	}
	objects := []string{
		catalog,
		"<< /Type /Pages /Kids [3 0 R 4 0 R 7 0 R] /Count 3 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Annots [5 0 R 6 0 R] >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] >>",
		"<< /Type /Annot /Subtype /Link /Rect [10 10 50 50] /Dest [4 0 R /Fit] >>",
		"<< /Type /Annot /Subtype /Link /Rect [60 10 100 50] /A << /S /GoTo /D [4 0 R /Fit] >> >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Annots [8 0 R 9 0 R 10 0 R 11 0 R 12 0 R 13 0 R 14 0 R 15 0 R 16 0 R 17 0 R 18 0 R] >>",
		"<< /Type /Annot /Subtype /Link /Rect [10 10 50 50] /A << /S /URI /URI (" + absoluteURIPDF + ") >> >>",
		"<< /Type /Annot /Subtype /Link /Rect [60 10 100 50] /A << /S /URI /URI (" + relativeURI + ") >> >>",
		"<< /Type /Annot /Subtype /Link /Rect [110 10 150 50] /A << /S /GoToR /F (other.pdf) /D [0 /Fit] >> >>",
		"<< /Type /Annot /Subtype /Link /Rect [10 60 50 100] /A << /S /Launch /F (notes.txt) >> >>",
		"<< /Type /Annot /Subtype /Link /Rect [60 60 100 100] /A << /S /Named /N /NextPage >> >>",
		"<< /Type /Annot /Subtype /Link /Rect [110 60 150 100] >>",
		"<< /Type /Annot /Subtype /Link /Rect [10 110 50 150] /A << /S /URI /URI () >> >>",
		"<< /Type /Annot /Subtype /Link /Rect [60 110 100 150] /Dest [1 0 R /Fit] >>",
		"<< /Type /Annot /Subtype /Link /Rect [110 110 150 150] /A << /S /URI /URI (" + colonRelativeURI + ") >> >>",
		"<< /Type /Annot /Subtype /Link /Rect [10 160 50 200] /A << /S /URI /URI (" + leadingColonURI + ") >> >>",
		"<< /Type /Annot /Subtype /Link /Rect [60 160 100 200] /A << /S /URI /URI (" + invalidUTF8URIPDF + ") >> >>",
	}

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, obj := range objects {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}

	xrefOffset := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xrefOffset)
	return buf.Bytes()
}

// actionPageLinks returns the links Links must report for actionLinkPage when
// PDFium concatenates basePrefix (the catalog's /URI /Base, or "" without one)
// in front of the URIs it applies to: those without ':' and those starting
// with ':'.
func actionPageLinks(basePrefix string) []PageLink {
	return []PageLink{
		{Index: 0, TargetPage: noTargetPage, URI: absoluteURI},
		{Index: 1, TargetPage: noTargetPage, URI: basePrefix + relativeURI},
		{Index: 2, TargetPage: noTargetPage, URI: ""},
		{Index: 3, TargetPage: noTargetPage, URI: ""},
		{Index: 4, TargetPage: noTargetPage, URI: ""},
		{Index: 5, TargetPage: noTargetPage, URI: ""},
		{Index: 6, TargetPage: noTargetPage, URI: basePrefix},
		{Index: 7, TargetPage: noTargetPage, URI: ""},
		{Index: 8, TargetPage: noTargetPage, URI: colonRelativeURI},
		{Index: 9, TargetPage: noTargetPage, URI: basePrefix + leadingColonURI},
		{Index: 10, TargetPage: noTargetPage, URI: invalidUTF8URI},
	}
}

func openLinkPDF(t *testing.T, base string) (*Document, func()) {
	t.Helper()
	doc, cleanup, err := OpenDocumentFromBytes(buildLinkPDF(base))
	if err != nil {
		t.Fatalf("OpenDocumentFromBytes: %v", err)
	}
	return doc, cleanup
}

var errInjected = errors.New("injected pdfium failure")

type fault int

const (
	faultError fault = iota + 1
	faultNilResponse
	faultMissingNextStartPos
	faultMissingURIPath
)

func (f fault) String() string {
	switch f {
	case faultError:
		return "error"
	case faultNilResponse:
		return "nil-response"
	case faultMissingNextStartPos:
		return "missing-next-start-pos"
	case faultMissingURIPath:
		return "missing-uri-path"
	}
	return fmt.Sprintf("fault(%d)", int(f))
}

// faultInjectingPdfium forwards every call to a real PDFium instance except
// the one named method, which fails with the configured fault. It records
// whether the document and the instance were closed.
type faultInjectingPdfium struct {
	pdfium.Pdfium
	method         string
	fault          fault
	documentClosed bool
	instanceClosed bool
}

func (f *faultInjectingPdfium) OpenDocument(req *requests.OpenDocument) (*responses.OpenDocument, error) {
	return inject(f, "OpenDocument", req, f.Pdfium.OpenDocument)
}

func (f *faultInjectingPdfium) FPDF_GetPageCount(req *requests.FPDF_GetPageCount) (*responses.FPDF_GetPageCount, error) {
	return inject(f, "FPDF_GetPageCount", req, f.Pdfium.FPDF_GetPageCount)
}

func (f *faultInjectingPdfium) FPDF_CloseDocument(req *requests.FPDF_CloseDocument) (*responses.FPDF_CloseDocument, error) {
	f.documentClosed = true
	return f.Pdfium.FPDF_CloseDocument(req)
}

func (f *faultInjectingPdfium) Close() error {
	f.instanceClosed = true
	return f.Pdfium.Close()
}

func inject[Req, Resp any](f *faultInjectingPdfium, method string, req Req, call func(Req) (*Resp, error)) (*Resp, error) {
	if f.method != method {
		return call(req)
	}
	switch f.fault {
	case faultError:
		return nil, errInjected
	case faultNilResponse:
		return nil, nil
	}
	return call(req)
}

func (f *faultInjectingPdfium) GetPageText(req *requests.GetPageText) (*responses.GetPageText, error) {
	return inject(f, "GetPageText", req, f.Pdfium.GetPageText)
}

func (f *faultInjectingPdfium) FPDFLink_Enumerate(req *requests.FPDFLink_Enumerate) (*responses.FPDFLink_Enumerate, error) {
	resp, err := inject(f, "FPDFLink_Enumerate", req, f.Pdfium.FPDFLink_Enumerate)
	if f.method == "FPDFLink_Enumerate" && f.fault == faultMissingNextStartPos && resp != nil {
		resp.NextStartPos = nil
	}
	return resp, err
}

func (f *faultInjectingPdfium) FPDFLink_GetDest(req *requests.FPDFLink_GetDest) (*responses.FPDFLink_GetDest, error) {
	return inject(f, "FPDFLink_GetDest", req, f.Pdfium.FPDFLink_GetDest)
}

func (f *faultInjectingPdfium) FPDFDest_GetDestPageIndex(req *requests.FPDFDest_GetDestPageIndex) (*responses.FPDFDest_GetDestPageIndex, error) {
	return inject(f, "FPDFDest_GetDestPageIndex", req, f.Pdfium.FPDFDest_GetDestPageIndex)
}

func (f *faultInjectingPdfium) FPDFLink_GetAction(req *requests.FPDFLink_GetAction) (*responses.FPDFLink_GetAction, error) {
	return inject(f, "FPDFLink_GetAction", req, f.Pdfium.FPDFLink_GetAction)
}

func (f *faultInjectingPdfium) FPDFAction_GetDest(req *requests.FPDFAction_GetDest) (*responses.FPDFAction_GetDest, error) {
	return inject(f, "FPDFAction_GetDest", req, f.Pdfium.FPDFAction_GetDest)
}

func (f *faultInjectingPdfium) FPDFAction_GetType(req *requests.FPDFAction_GetType) (*responses.FPDFAction_GetType, error) {
	return inject(f, "FPDFAction_GetType", req, f.Pdfium.FPDFAction_GetType)
}

func (f *faultInjectingPdfium) FPDFAction_GetURIPath(req *requests.FPDFAction_GetURIPath) (*responses.FPDFAction_GetURIPath, error) {
	resp, err := inject(f, "FPDFAction_GetURIPath", req, f.Pdfium.FPDFAction_GetURIPath)
	if f.method == "FPDFAction_GetURIPath" && f.fault == faultMissingURIPath && resp != nil {
		resp.URIPath = nil
	}
	return resp, err
}

func TestDocumentLinks_ResolvesTargets(t *testing.T) {
	internalURI := fmt.Sprintf("#page=%d", linkTargetPage+1)
	tests := []struct {
		name string
		base string
		page int
		want []PageLink
	}{
		{"direct and action destinations", uriBase, linkSourcePage, []PageLink{
			{Index: 0, TargetPage: linkTargetPage, URI: internalURI},
			{Index: 1, TargetPage: linkTargetPage, URI: internalURI},
		}},
		{"page without links", uriBase, linkTargetPage, nil},
		{"links without in-document destination, catalog base", uriBase, actionLinkPage, actionPageLinks(uriBase)},
		{"links without in-document destination, no catalog base", "", actionLinkPage, actionPageLinks("")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, cleanup := openLinkPDF(t, tt.base)
			defer cleanup()

			got, err := doc.Links(tt.page)
			if err != nil {
				t.Fatalf("Links(%d) error = %v, want nil", tt.page, err)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("Links(%d) =\n%#v\nwant\n%#v", tt.page, got, tt.want)
			}
		})
	}
}

func TestDocumentLinks_ClosedDocumentFails(t *testing.T) {
	doc, cleanup := openLinkPDF(t, uriBase)
	cleanup()

	links, err := doc.Links(linkSourcePage)
	if err == nil {
		t.Fatalf("Links on closed document = %+v, nil error; want error", links)
	}
	if links != nil {
		t.Fatalf("Links on closed document returned partial list %+v", links)
	}
	if !strings.Contains(err.Error(), "instance is closed") {
		t.Fatalf("Links on closed document error = %q, want PDFium closed-instance cause", err)
	}
}

func TestDocumentLinks_RequestFailures(t *testing.T) {
	tests := []struct {
		method string
		fault  fault
		page   int
		want   error
	}{
		{"FPDFLink_Enumerate", faultError, linkSourcePage, errInjected},
		{"FPDFLink_Enumerate", faultNilResponse, linkSourcePage, errMissingResponse},
		{"FPDFLink_Enumerate", faultMissingNextStartPos, linkSourcePage, errMissingNextStartPos},
		{"FPDFLink_GetDest", faultError, linkSourcePage, errInjected},
		{"FPDFLink_GetDest", faultNilResponse, linkSourcePage, errMissingResponse},
		{"FPDFDest_GetDestPageIndex", faultError, linkSourcePage, errInjected},
		{"FPDFDest_GetDestPageIndex", faultNilResponse, linkSourcePage, errMissingResponse},
		{"FPDFLink_GetAction", faultError, actionLinkPage, errInjected},
		{"FPDFLink_GetAction", faultNilResponse, actionLinkPage, errMissingResponse},
		{"FPDFAction_GetType", faultError, actionLinkPage, errInjected},
		{"FPDFAction_GetType", faultNilResponse, actionLinkPage, errMissingResponse},
		{"FPDFAction_GetDest", faultError, linkSourcePage, errInjected},
		{"FPDFAction_GetDest", faultNilResponse, linkSourcePage, errMissingResponse},
		{"FPDFAction_GetURIPath", faultError, actionLinkPage, errInjected},
		{"FPDFAction_GetURIPath", faultNilResponse, actionLinkPage, errMissingResponse},
		{"FPDFAction_GetURIPath", faultMissingURIPath, actionLinkPage, errMissingURIPath},
	}
	for _, tt := range tests {
		t.Run(tt.method+"/"+tt.fault.String(), func(t *testing.T) {
			doc, cleanup := openLinkPDF(t, uriBase)
			defer cleanup()
			doc.instance = &faultInjectingPdfium{Pdfium: doc.instance, method: tt.method, fault: tt.fault}

			links, err := doc.Links(tt.page)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Links(%d) with failing %s = %+v, error %v; want error wrapping %v", tt.page, tt.method, links, err, tt.want)
			}
			if links != nil {
				t.Fatalf("Links(%d) with failing %s returned partial list %+v", tt.page, tt.method, links)
			}
			if !strings.Contains(err.Error(), fmt.Sprintf("page %d", tt.page)) {
				t.Fatalf("Links(%d) with failing %s error = %q, want page context", tt.page, tt.method, err)
			}
		})
	}
}

func TestOpenDocument_RequestFailures(t *testing.T) {
	pool, err := getPool()
	if err != nil {
		t.Fatalf("getPool: %v", err)
	}

	// The OpenDocument nil-response row runs last: without a nil check it
	// dereferences the missing response and panics.
	tests := []struct {
		method             string
		fault              fault
		want               error
		wantDocumentClosed bool
	}{
		{"FPDF_GetPageCount", faultError, errInjected, true},
		{"FPDF_GetPageCount", faultNilResponse, errMissingResponse, true},
		{"OpenDocument", faultError, errInjected, false},
		{"OpenDocument", faultNilResponse, errMissingResponse, false},
	}
	for _, tt := range tests {
		t.Run(tt.method+"/"+tt.fault.String(), func(t *testing.T) {
			instance, err := pool.GetInstance(instanceTimeout)
			if err != nil {
				t.Fatalf("GetInstance: %v", err)
			}
			f := &faultInjectingPdfium{Pdfium: instance, method: tt.method, fault: tt.fault}

			doc, cleanup, err := openDocument(f, buildLinkPDF(uriBase))
			if err == nil {
				cleanup()
				t.Fatalf("openDocument with failing %s = %d pages, nil error; want error wrapping %v", tt.method, doc.NumPage(), tt.want)
			}
			if !f.instanceClosed {
				_ = instance.Close() // release the pool slot so later tests can run
				t.Fatalf("openDocument with failing %s did not close the instance", tt.method)
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("openDocument with failing %s error = %v, want wrapping %v", tt.method, err, tt.want)
			}
			if doc != nil || cleanup != nil {
				t.Fatalf("openDocument with failing %s returned a document or cleanup function", tt.method)
			}
			if f.documentClosed != tt.wantDocumentClosed {
				t.Fatalf("openDocument with failing %s closed document = %t, want %t", tt.method, f.documentClosed, tt.wantDocumentClosed)
			}
		})
	}
}

func TestDocumentText_RequestFailures(t *testing.T) {
	tests := []struct {
		fault fault
		want  error
	}{
		{faultError, errInjected},
		{faultNilResponse, errMissingResponse},
	}
	for _, tt := range tests {
		t.Run(tt.fault.String(), func(t *testing.T) {
			doc, cleanup := openLinkPDF(t, uriBase)
			defer cleanup()
			doc.instance = &faultInjectingPdfium{Pdfium: doc.instance, method: "GetPageText", fault: tt.fault}

			text, err := doc.Text(linkSourcePage)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Text(%d) with failing GetPageText = %q, error %v; want error wrapping %v", linkSourcePage, text, err, tt.want)
			}
		})
	}
}
