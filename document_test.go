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
	uriLinkPage    = 2
)

// buildLinkPDF builds a three-page PDF. Page 0 carries two link annotations to
// page 1: one with a direct /Dest and one with a /GoTo action. Page 1 has no
// annotations. Page 2 carries a /URI action link, which has no destination,
// so resolving it reaches FPDFLink_GetAction and FPDFAction_GetDest.
func buildLinkPDF() []byte {
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R 4 0 R 7 0 R] /Count 3 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Annots [5 0 R 6 0 R] >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] >>",
		"<< /Type /Annot /Subtype /Link /Rect [10 10 50 50] /Dest [4 0 R /Fit] >>",
		"<< /Type /Annot /Subtype /Link /Rect [60 10 100 50] /A << /S /GoTo /D [4 0 R /Fit] >> >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Annots [8 0 R] >>",
		"<< /Type /Annot /Subtype /Link /Rect [10 10 50 50] /A << /S /URI /URI (https://example.com/) >> >>",
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

func openLinkPDF(t *testing.T) (*Document, func()) {
	t.Helper()
	doc, cleanup, err := OpenDocumentFromBytes(buildLinkPDF())
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
)

func (f fault) String() string {
	switch f {
	case faultError:
		return "error"
	case faultNilResponse:
		return "nil-response"
	case faultMissingNextStartPos:
		return "missing-next-start-pos"
	}
	return fmt.Sprintf("fault(%d)", int(f))
}

// faultInjectingPdfium forwards every call to a real PDFium instance except
// the one named method, which fails with the configured fault.
type faultInjectingPdfium struct {
	pdfium.Pdfium
	method string
	fault  fault
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

func TestDocumentLinks_ResolvesTargets(t *testing.T) {
	doc, cleanup := openLinkPDF(t)
	defer cleanup()

	tests := []struct {
		name string
		page int
		want []PageLink
	}{
		{
			name: "direct and action destinations",
			page: linkSourcePage,
			want: []PageLink{
				{Index: 0, TargetPage: linkTargetPage, URI: "#page=2"},
				{Index: 1, TargetPage: linkTargetPage, URI: "#page=2"},
			},
		},
		{
			name: "page without links",
			page: linkTargetPage,
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := doc.Links(tt.page)
			if err != nil {
				t.Fatalf("Links(%d) error = %v, want nil", tt.page, err)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("Links(%d) = %+v, want %+v", tt.page, got, tt.want)
			}
		})
	}
}

func TestDocumentLinks_ClosedDocumentFails(t *testing.T) {
	doc, cleanup := openLinkPDF(t)
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
		{"FPDFLink_GetAction", faultError, uriLinkPage, errInjected},
		{"FPDFLink_GetAction", faultNilResponse, uriLinkPage, errMissingResponse},
		{"FPDFAction_GetDest", faultError, uriLinkPage, errInjected},
		{"FPDFAction_GetDest", faultNilResponse, uriLinkPage, errMissingResponse},
	}
	for _, tt := range tests {
		t.Run(tt.method+"/"+tt.fault.String(), func(t *testing.T) {
			doc, cleanup := openLinkPDF(t)
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
			doc, cleanup := openLinkPDF(t)
			defer cleanup()
			doc.instance = &faultInjectingPdfium{Pdfium: doc.instance, method: "GetPageText", fault: tt.fault}

			text, err := doc.Text(linkSourcePage)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Text(%d) with failing GetPageText = %q, error %v; want error wrapping %v", linkSourcePage, text, err, tt.want)
			}
		})
	}
}
