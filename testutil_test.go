package render_test

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/alexgorbatchev/go-remarkable-render"
	"github.com/alexgorbatchev/go-rmscene"
)

// buildMinimalPDF builds a valid PDF 1.4 byte slice with the given pages of text lines.
func buildMinimalPDF(pages ...[]string) []byte {
	numPages := len(pages)
	var kids strings.Builder
	for i := 0; i < numPages; i++ {
		pageObjID := 4 + 2*i
		fmt.Fprintf(&kids, "%d 0 R ", pageObjID)
	}
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.TrimSpace(kids.String()), numPages),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}

	// Pages and Contents
	for i, lines := range pages {
		contentObjID := 5 + 2*i

		// Content stream
		var streamBuf bytes.Buffer
		streamBuf.WriteString("BT\n/F1 12 Tf\n16 TL\n72 500 Td\n")
		for _, line := range lines {
			escaped := strings.ReplaceAll(line, "\\", "\\\\")
			escaped = strings.ReplaceAll(escaped, "(", "\\(")
			escaped = strings.ReplaceAll(escaped, ")", "\\)")
			fmt.Fprintf(&streamBuf, "(%s) Tj\nT*\n", escaped)
		}
		streamBuf.WriteString("ET\n")

		objects = append(objects,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 447.874 595.275] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", contentObjID),
			fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", streamBuf.Len(), streamBuf.String()),
		)
	}
	return render.EncodeTestPDF(objects)
}

// buildSampleRMStrokes generates valid v6 .rm binary bytes with ballpoint and highlighter strokes.
func buildSampleRMStrokes() ([]byte, error) {
	var buf bytes.Buffer
	w := rmscene.NewDataWriter(&buf)
	if err := w.WriteHeader(); err != nil {
		return nil, err
	}

	err := w.WriteBlock(rmscene.BlockTypeMigrationInfo, 1, 1, func(bw *rmscene.DataWriter) error {
		if err := bw.WriteId(1, rmscene.CrdtId{Part1: 1, Part2: 1}); err != nil {
			return err
		}
		return bw.WriteTaggedBool(2, true)
	})
	if err != nil {
		return nil, err
	}

	ballpointLine := &rmscene.Line{
		Color: rmscene.PenColorBlack,
		Tool:  rmscene.PenToolBallpoint1,
		Points: []rmscene.Point{
			{X: 100.0, Y: 150.0, Speed: 120, Direction: 45, Width: 12, Pressure: 180},
			{X: 120.0, Y: 180.0, Speed: 125, Direction: 48, Width: 12, Pressure: 185},
			{X: 150.0, Y: 200.0, Speed: 130, Direction: 50, Width: 12, Pressure: 190},
		},
		ThicknessScale: 1.5,
	}
	ballpointBlock := &rmscene.SceneLineItemBlock{
		ParentID: rmscene.CrdtId{Part1: 1, Part2: 1},
		Item: rmscene.Item[*rmscene.Line]{
			ItemID: rmscene.CrdtId{Part1: 1, Part2: 10},
			Value:  ballpointLine,
		},
	}
	if err := w.WriteSceneLineItemBlock(ballpointBlock, 2); err != nil {
		return nil, err
	}

	hlLine := &rmscene.Line{
		Color: rmscene.PenColorHighlight,
		Tool:  rmscene.PenToolHighlighter1,
		Points: []rmscene.Point{
			{X: 80.0, Y: 140.0, Speed: 60, Direction: 0, Width: 20, Pressure: 200},
			{X: 160.0, Y: 140.0, Speed: 60, Direction: 0, Width: 20, Pressure: 200},
		},
		ThicknessScale: 1.0,
	}
	hlBlock := &rmscene.SceneLineItemBlock{
		ParentID: rmscene.CrdtId{Part1: 1, Part2: 1},
		Item: rmscene.Item[*rmscene.Line]{
			ItemID: rmscene.CrdtId{Part1: 1, Part2: 11},
			Value:  hlLine,
		},
		ExtraValueData: []byte{0x84, 0x01, 0x19, 0xF7, 0xFB, 0x00}, // Yellow
	}
	if err := w.WriteSceneLineItemBlock(hlBlock, 2); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
