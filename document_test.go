package render

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/responses"
)

const (
	linkSourcePage   = 0
	linkTargetPage   = 1
	actionLinkPage   = 2
	labelledLinkPage = 3
	missingRectPage  = 4
)

// labelledPageContent draws 12pt Helvetica words at known baselines on
// labelledLinkPage. Each word's advance width follows from the standard
// Helvetica metrics (in 1/1000 em): "Partial" starts at x=20, so "P" (667)
// spans 20-28.0 and "a" (556) spans 28.0-34.7; "Mon Tue" starts at x=100, so
// "Mon" (1945) spans 100-123.3 and the space (278) spans 123.3-126.7; "Year"
// sits on baseline 50 and "2026" on baseline 36. "AB" uses font F2, whose
// astralToUnicodeCMap maps "A" to astralText's first code point.
const labelledPageContent = "BT /F1 12 Tf 20 150 Td (Notes) Tj ET\n" +
	"BT /F1 12 Tf 100 150 Td (Standup) Tj ET\n" +
	"BT /F1 12 Tf 20 100 Td (Partial) Tj ET\n" +
	"BT /F1 12 Tf 100 100 Td (Mon Tue) Tj ET\n" +
	"BT /F1 12 Tf 20 50 Td (Year) Tj 0 -14 Td (2026) Tj ET\n" +
	"BT /F1 12 Tf 100 50 Td (Back) Tj ET\n" +
	"BT /F2 12 Tf 100 15 Td (AB) Tj ET\n"

// astralText is the text of "AB" in font F2: U+1F600, outside the Basic
// Multilingual Plane so PDFium reports it as a UTF-16 surrogate pair, and "B".
const astralText = "\U0001F600B"

// astralToUnicodeCMap is the /ToUnicode CMap of font F2.
const astralToUnicodeCMap = "/CIDInit /ProcSet findresource begin\n" +
	"12 dict begin\nbegincmap\n/CMapName /Astral def\n/CMapType 2 def\n" +
	"1 begincodespacerange <00> <FF> endcodespacerange\n" +
	"2 beginbfchar\n<41> <D83DDE00>\n<42> <0042>\nendbfchar\n" +
	"endcmap\nCMapName currentdict /CMap defineresource pop\nend\nend\n"

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

// buildLinkPDF builds a five-page PDF whose catalog carries /URI /Base base,
// or no /URI dictionary when base is empty. Page 0 carries two link
// annotations to page 1: one with a direct /Dest and one with a /GoTo action.
// Page 1 has no annotations. Page 2 carries the links actionPageLinks
// describes, none of which has an in-document destination. Pages 0-2 have no
// content, so no link there covers text. Page 3 draws labelledPageContent and
// carries the links labelledPageLinks describes; link 5 gives its /Rect
// corners in reverse order. Page 4 carries a link without a /Rect.
func buildLinkPDF(base string) []byte {
	catalog := "<< /Type /Catalog /Pages 2 0 R >>"
	if base != "" {
		catalog = "<< /Type /Catalog /Pages 2 0 R /URI << /Base (" + base + ") >> >>"
	}
	objects := []string{
		catalog,
		"<< /Type /Pages /Kids [3 0 R 4 0 R 7 0 R 19 0 R 28 0 R] /Count 5 >>",
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
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << /Font << /F1 21 0 R /F2 30 0 R >> >> /Contents 20 0 R /Annots [22 0 R 23 0 R 24 0 R 25 0 R 26 0 R 27 0 R 32 0 R] >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(labelledPageContent), labelledPageContent),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		"<< /Type /Annot /Subtype /Link /Rect [15 145 60 165] /Dest [4 0 R /Fit] >>",
		"<< /Type /Annot /Subtype /Link /Rect [95 145 150 165] /A << /S /GoTo /D [4 0 R /Fit] >> >>",
		"<< /Type /Annot /Subtype /Link /Rect [15 95 31 115] /A << /S /URI /URI (" + relativeURI + ") >> >>",
		"<< /Type /Annot /Subtype /Link /Rect [95 95 124.5 115] /Dest [4 0 R /Fit] >>",
		"<< /Type /Annot /Subtype /Link /Rect [15 30 60 65] /Dest [3 0 R /Fit] >>",
		"<< /Type /Annot /Subtype /Link /Rect [150.25 65.5 95.75 45] /Dest [4 0 R /Fit] >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Annots [29 0 R] >>",
		"<< /Type /Annot /Subtype /Link /Dest [4 0 R /Fit] >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /ToUnicode 31 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(astralToUnicodeCMap), astralToUnicodeCMap),
		"<< /Type /Annot /Subtype /Link /Rect [95 10 150 30] /Dest [4 0 R /Fit] >>",
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
		{Index: 0, TargetPage: noTargetPage, URI: absoluteURI, Rect: Rect{Left: 10, Bottom: 10, Right: 50, Top: 50}},
		{Index: 1, TargetPage: noTargetPage, URI: basePrefix + relativeURI, Rect: Rect{Left: 60, Bottom: 10, Right: 100, Top: 50}},
		{Index: 2, TargetPage: noTargetPage, URI: "", Rect: Rect{Left: 110, Bottom: 10, Right: 150, Top: 50}},
		{Index: 3, TargetPage: noTargetPage, URI: "", Rect: Rect{Left: 10, Bottom: 60, Right: 50, Top: 100}},
		{Index: 4, TargetPage: noTargetPage, URI: "", Rect: Rect{Left: 60, Bottom: 60, Right: 100, Top: 100}},
		{Index: 5, TargetPage: noTargetPage, URI: "", Rect: Rect{Left: 110, Bottom: 60, Right: 150, Top: 100}},
		{Index: 6, TargetPage: noTargetPage, URI: basePrefix, Rect: Rect{Left: 10, Bottom: 110, Right: 50, Top: 150}},
		{Index: 7, TargetPage: noTargetPage, URI: "", Rect: Rect{Left: 60, Bottom: 110, Right: 100, Top: 150}},
		{Index: 8, TargetPage: noTargetPage, URI: colonRelativeURI, Rect: Rect{Left: 110, Bottom: 110, Right: 150, Top: 150}},
		{Index: 9, TargetPage: noTargetPage, URI: basePrefix + leadingColonURI, Rect: Rect{Left: 10, Bottom: 160, Right: 50, Top: 200}},
		{Index: 10, TargetPage: noTargetPage, URI: invalidUTF8URI, Rect: Rect{Left: 60, Bottom: 160, Right: 100, Top: 200}},
	}
}

// labelledPageLinks returns the links Links must report for labelledLinkPage
// in a document whose catalog has /URI /Base uriBase. PDFium reports every
// character whose box overlaps the rectangle, so link 2 yields the whole "a"
// although its rectangle ends inside it. PDFium's raw text is "Notes " for
// link 0 and "Mon " for link 3, with the following space outside the
// rectangle, and "Year\r\n2026" for link 4; Links collapses and trims that
// white space. Link 5's /Rect lists its corners in reverse order. Link 6
// covers astralText, which Links reports as UTF-8.
func labelledPageLinks() []PageLink {
	internalURI := fmt.Sprintf("#page=%d", linkTargetPage+1)
	return []PageLink{
		{Index: 0, TargetPage: linkTargetPage, URI: internalURI, Rect: Rect{Left: 15, Bottom: 145, Right: 60, Top: 165}, Text: "Notes"},
		{Index: 1, TargetPage: linkTargetPage, URI: internalURI, Rect: Rect{Left: 95, Bottom: 145, Right: 150, Top: 165}, Text: "Standup"},
		{Index: 2, TargetPage: noTargetPage, URI: uriBase + relativeURI, Rect: Rect{Left: 15, Bottom: 95, Right: 31, Top: 115}, Text: "Pa"},
		{Index: 3, TargetPage: linkTargetPage, URI: internalURI, Rect: Rect{Left: 95, Bottom: 95, Right: 124.5, Top: 115}, Text: "Mon"},
		{Index: 4, TargetPage: linkSourcePage, URI: fmt.Sprintf("#page=%d", linkSourcePage+1), Rect: Rect{Left: 15, Bottom: 30, Right: 60, Top: 65}, Text: "Year 2026"},
		{Index: 5, TargetPage: linkTargetPage, URI: internalURI, Rect: Rect{Left: 95.75, Bottom: 45, Right: 150.25, Top: 65.5}, Text: "Back"},
		{Index: 6, TargetPage: linkTargetPage, URI: internalURI, Rect: Rect{Left: 95, Bottom: 10, Right: 150, Top: 30}, Text: astralText},
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
	faultMissingRect
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
	case faultMissingRect:
		return "missing-rect"
	}
	return fmt.Sprintf("fault(%d)", int(f))
}

// faultInjectingPdfium forwards every call to a real PDFium instance except
// the one named method, which fails with the configured fault. It records
// whether the document and the instance were closed, and which text pages are
// loaded and not yet closed.
type faultInjectingPdfium struct {
	pdfium.Pdfium
	method         string
	fault          fault
	documentClosed bool
	instanceClosed bool
	openTextPages  map[references.FPDF_TEXTPAGE]bool
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

func (f *faultInjectingPdfium) FPDFLink_GetAnnotRect(req *requests.FPDFLink_GetAnnotRect) (*responses.FPDFLink_GetAnnotRect, error) {
	resp, err := inject(f, "FPDFLink_GetAnnotRect", req, f.Pdfium.FPDFLink_GetAnnotRect)
	if f.method == "FPDFLink_GetAnnotRect" && f.fault == faultMissingRect && resp != nil {
		resp.Rect = nil
	}
	return resp, err
}

func (f *faultInjectingPdfium) FPDFText_LoadPage(req *requests.FPDFText_LoadPage) (*responses.FPDFText_LoadPage, error) {
	resp, err := inject(f, "FPDFText_LoadPage", req, f.Pdfium.FPDFText_LoadPage)
	if err == nil && resp != nil {
		if f.openTextPages == nil {
			f.openTextPages = map[references.FPDF_TEXTPAGE]bool{}
		}
		f.openTextPages[resp.TextPage] = true
	}
	return resp, err
}

func (f *faultInjectingPdfium) FPDFText_GetBoundedText(req *requests.FPDFText_GetBoundedText) (*responses.FPDFText_GetBoundedText, error) {
	return inject(f, "FPDFText_GetBoundedText", req, f.Pdfium.FPDFText_GetBoundedText)
}

// FPDFText_ClosePage closes the text page before injecting a fault, so a test
// can tell a failed close request from a text page that was never closed.
func (f *faultInjectingPdfium) FPDFText_ClosePage(req *requests.FPDFText_ClosePage) (*responses.FPDFText_ClosePage, error) {
	resp, err := f.Pdfium.FPDFText_ClosePage(req)
	if err == nil {
		delete(f.openTextPages, req.TextPage)
	}
	return inject(f, "FPDFText_ClosePage", req, func(*requests.FPDFText_ClosePage) (*responses.FPDFText_ClosePage, error) {
		return resp, err
	})
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
			{Index: 0, TargetPage: linkTargetPage, URI: internalURI, Rect: Rect{Left: 10, Bottom: 10, Right: 50, Top: 50}},
			{Index: 1, TargetPage: linkTargetPage, URI: internalURI, Rect: Rect{Left: 60, Bottom: 10, Right: 100, Top: 50}},
		}},
		{"page without links", uriBase, linkTargetPage, nil},
		{"links without in-document destination, catalog base", uriBase, actionLinkPage, actionPageLinks(uriBase)},
		{"links without in-document destination, no catalog base", "", actionLinkPage, actionPageLinks("")},
		{"links over page text", uriBase, labelledLinkPage, labelledPageLinks()},
		// PDFium reports a missing /Rect as an all-zero rectangle, which
		// covers no text.
		{"link without rectangle", uriBase, missingRectPage, []PageLink{
			{Index: 0, TargetPage: linkTargetPage, URI: internalURI},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, cleanup := openLinkPDF(t, tt.base)
			defer cleanup()
			tracker := &faultInjectingPdfium{Pdfium: doc.instance}
			doc.instance = tracker

			got, err := doc.Links(tt.page)
			if err != nil {
				t.Fatalf("Links(%d) error = %v, want nil", tt.page, err)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("Links(%d) =\n%#v\nwant\n%#v", tt.page, got, tt.want)
			}
			if len(tracker.openTextPages) != 0 {
				t.Fatalf("Links(%d) left %d text pages open", tt.page, len(tracker.openTextPages))
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
		{"FPDFLink_GetAnnotRect", faultError, labelledLinkPage, errInjected},
		{"FPDFLink_GetAnnotRect", faultNilResponse, labelledLinkPage, errMissingResponse},
		{"FPDFLink_GetAnnotRect", faultMissingRect, labelledLinkPage, errMissingRect},
		{"FPDFText_LoadPage", faultError, labelledLinkPage, errInjected},
		{"FPDFText_LoadPage", faultNilResponse, labelledLinkPage, errMissingResponse},
		{"FPDFText_GetBoundedText", faultError, labelledLinkPage, errInjected},
		{"FPDFText_GetBoundedText", faultNilResponse, labelledLinkPage, errMissingResponse},
		{"FPDFText_ClosePage", faultError, labelledLinkPage, errInjected},
		{"FPDFText_ClosePage", faultNilResponse, labelledLinkPage, errMissingResponse},
	}
	for _, tt := range tests {
		t.Run(tt.method+"/"+tt.fault.String(), func(t *testing.T) {
			doc, cleanup := openLinkPDF(t, uriBase)
			defer cleanup()
			f := &faultInjectingPdfium{Pdfium: doc.instance, method: tt.method, fault: tt.fault}
			doc.instance = f

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
			if len(f.openTextPages) != 0 {
				t.Fatalf("Links(%d) with failing %s left %d text pages open", tt.page, tt.method, len(f.openTextPages))
			}
		})
	}
}

// TestDocumentLinks_FixtureLinkText reads the link of a MuPDF-written planner
// page whose rectangle covers only the start of the header word "Sep".
func TestDocumentLinks_FixtureLinkText(t *testing.T) {
	data, err := os.ReadFile("testdata/linked_pages.pdf")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	doc, cleanup, err := OpenDocumentFromBytes(data)
	if err != nil {
		t.Fatalf("OpenDocumentFromBytes: %v", err)
	}
	defer cleanup()

	got, err := doc.Links(0)
	if err != nil {
		t.Fatalf("Links(0) error = %v, want nil", err)
	}
	want := []PageLink{
		{Index: 0, TargetPage: noTargetPage, URI: "#page=2", Rect: Rect{Left: 10, Bottom: 545.2756, Right: 50, Top: 585.2756}, Text: "Se"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Links(0) =\n%#v\nwant\n%#v", got, want)
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
