# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "pymupdf>=1.28.0",
#     "rmscene>=0.6.1",
#     "rmc>=0.3.0",
# ]
# ///
"""Regenerate the golden reference render for go-remarkable-render E2E testing.

Usage:
    uv run testdata/generate_golden.py
"""

from __future__ import annotations

import io
import re
from pathlib import Path

import pymupdf
import rmscene
import rmscene.scene_items as si
import rmc.exporters.writing_tools as wt
from rmc.exporters.svg import xx, yy, scale

# Patch rmc's writing tools palette for Paper Pro colors
if hasattr(wt, "RM_PALETTE"):
    wt.RM_PALETTE[si.PenColor.HIGHLIGHT] = (251, 247, 25)
    wt.RM_PALETTE[9] = (251, 247, 25)
    wt.RM_PALETTE[10] = (161, 216, 125)  # Green 2
    wt.RM_PALETTE[11] = (139, 208, 229)  # Cyan
    wt.RM_PALETTE[12] = (242, 158, 255)  # Magenta
    wt.RM_PALETTE[13] = (247, 232, 81)   # Yellow 2

TESTDATA_DIR = Path(__file__).parent.resolve()
TEMPLATE_PDF = TESTDATA_DIR / "oct1_notes_template.pdf"
STROKES_RM = TESTDATA_DIR / "oct1_notes_strokes.rm"
GOLDEN_PNG = TESTDATA_DIR / "oct1_notes_golden.png"


def extract_highlighter_color(extra_bytes: bytes) -> tuple[int, int, int, float]:
    """Extract 24-bit RGB color and opacity from Paper Pro extra_value_data on highlighter/shader strokes."""
    if extra_bytes:
        idx = extra_bytes.find(b"\x84\x01")
        if idx != -1 and len(extra_bytes) >= idx + 6:
            b_val, g_val, r_val, a_val = extra_bytes[idx + 2 : idx + 6]
            alpha = 0.45
            if 0 < a_val < 255:
                alpha = a_val / 255.0
            return (r_val, g_val, b_val, alpha)
    return (255, 235, 59, 0.45)


def render_rm_to_svg(rm_bytes: bytes, width_pt: float, height_pt: float) -> str:
    """Parse v6 .rm binary stroke data and render it to layered SVG string."""
    blocks = list(rmscene.read_blocks(io.BytesIO(rm_bytes)))

    highlighters: list[tuple[si.Line, tuple[int, int, int], float, float]] = []
    pen_strokes: list[si.Line] = []

    for b in blocks:
        if type(b).__name__ == "SceneLineItemBlock" and b.item.value is not None:
            line = b.item.value
            if "HIGHLIGHT" in line.tool.name or "SHADER" in line.tool.name:
                extra = getattr(b, "extra_value_data", b"")
                r, g, b_c, alpha = extract_highlighter_color(extra)
                stroke_w = 16.0 if "SHADER" in line.tool.name else 10.0
                highlighters.append((line, (r, g, b_c), alpha, stroke_w))
            else:
                pen_strokes.append(line)

    out = io.StringIO()
    half_w = width_pt / 2.0
    out.write('<?xml version="1.0" encoding="UTF-8"?>\n')
    out.write(f'<svg xmlns="http://www.w3.org/2000/svg" height="{height_pt}" width="{width_pt}" viewBox="-{half_w:.3f} 0.0 {width_pt} {height_pt}">\n')

    # 1. Highlighters and Shader washes first (layer underneath ink)
    out.write('\t<g id="highlighters">\n')
    for item_line, (r, g, b), opacity, stroke_w in highlighters:
        pts = " ".join(f"{xx(p.x):.3f},{yy(p.y):.3f}" for p in item_line.points)
        out.write(f'\t\t<polyline fill="none" stroke="rgb({r},{g},{b})" stroke-opacity="{opacity:.2f}" stroke-width="{stroke_w:.1f}" stroke-linecap="square" stroke-linejoin="round" points="{pts}" />\n')
    out.write("\t</g>\n")

    # 2. Pen strokes on top
    out.write('\t<g id="pen_strokes">\n')
    for item_line in pen_strokes:
        pen = wt.Pen.create(item_line.tool.value, item_line.color.value, item_line.thickness_scale)
        last_x, last_y = -1.0, -1.0
        last_w = 0
        for idx, p in enumerate(item_line.points):
            if idx % pen.segment_length == 0:
                if last_x != -1.0:
                    out.write('" />\n')
                col = pen.get_segment_color(p.speed, p.direction, p.width, p.pressure, last_w)
                w = pen.get_segment_width(p.speed, p.direction, p.width, p.pressure, last_w)
                op = pen.get_segment_opacity(p.speed, p.direction, p.width, p.pressure, last_w)
                out.write(f'\t\t<polyline fill="none" stroke="{col}" stroke-opacity="{op}" stroke-width="{scale(w):.3f}" stroke-linecap="round" stroke-linejoin="round" points="')
                if last_x != -1.0:
                    out.write(f"{xx(last_x):.3f},{yy(last_y):.3f} ")
            last_x, last_y = p.x, p.y
            last_w = w
            out.write(f"{xx(p.x):.3f},{yy(p.y):.3f} ")
        out.write('" />\n')
    out.write("\t</g>\n")
    out.write("</svg>\n")

    return out.getvalue()


def generate_golden(
    template_pdf: Path = TEMPLATE_PDF,
    strokes_rm: Path = STROKES_RM,
    output_png: Path = GOLDEN_PNG,
    dpi: int = 200,
) -> Path:
    """Generate golden reference PNG from template PDF and .rm strokes."""
    if not template_pdf.exists():
        raise FileNotFoundError(f"Template PDF not found: {template_pdf}")
    if not strokes_rm.exists():
        raise FileNotFoundError(f"Stroke file not found: {strokes_rm}")

    doc = pymupdf.open(template_pdf)
    page = doc[0]
    rm_bytes = strokes_rm.read_bytes()

    svg_str = render_rm_to_svg(rm_bytes, width_pt=page.rect.width, height_pt=page.rect.height)
    if svg_str:
        svg_doc = pymupdf.open(stream=svg_str.encode("utf-8"), filetype="svg")
        stroke_pdf_bytes = svg_doc.convert_to_pdf()
        stroke_doc = pymupdf.open("pdf", stroke_pdf_bytes)
        page.show_pdf_page(page.rect, stroke_doc, 0)

    pix = page.get_pixmap(dpi=dpi)
    output_png.parent.mkdir(parents=True, exist_ok=True)
    output_png.write_bytes(pix.tobytes("png"))
    print(f"Saved golden reference: {output_png} ({output_png.stat().st_size} bytes)")
    return output_png


if __name__ == "__main__":
    generate_golden()
