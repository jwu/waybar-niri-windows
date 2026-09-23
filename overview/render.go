package overview

import (
	"fmt"
	"math"
	"wnw/procs"

	"github.com/gotk3/gotk3/cairo"
	"github.com/gotk3/gotk3/pango"
)

// RGBA is a colour with components in [0, 1].
type RGBA struct {
	R, G, B, A float64
}

func hex(value uint32, alpha float64) RGBA {
	return RGBA{
		R: float64((value>>16)&0xff) / 255,
		G: float64((value>>8)&0xff) / 255,
		B: float64(value&0xff) / 255,
		A: alpha,
	}
}

func (c RGBA) set(cr *cairo.Context) {
	cr.SetSourceRGBA(c.R, c.G, c.B, c.A)
}

func (c RGBA) alpha(a float64) RGBA {
	c.A = a
	return c
}

// Theme mirrors the "One Half Dark" palette used by waybar and ghostty.
type Theme struct {
	Font string

	Background RGBA

	Panel       RGBA
	PanelBorder RGBA

	Label    RGBA
	LabelDim RGBA

	Tile       RGBA
	TileUrgent RGBA

	Border       RGBA
	BorderUrgent RGBA

	Text    RGBA
	TextDim RGBA

	// Activity is the colour of the marker a working window gets; a window
	// that only worked recently gets the same colour, faded.
	Activity RGBA
	Red      RGBA

	HeaderIcon RGBA
	HeaderText RGBA

	LabelSize      float64
	AppSize        float64
	TitleSize      float64
	HeaderIconSize float64
	HeaderSize     float64
}

// DefaultTheme returns the dark theme.
func DefaultTheme() *Theme {
	return &Theme{
		Font: "FiraMono Nerd Font",

		Background: hex(0x1b1e23, 1),

		Panel:       hex(0x21252b, 1),
		PanelBorder: hex(0x3b4048, 1),

		Label:    hex(0xc8cdd6, 1),
		LabelDim: hex(0x5c6370, 1),

		Tile:       hex(0x3a4049, 1),
		TileUrgent: hex(0xe86671, 0.55),

		Border:       hex(0x4a5058, 1),
		BorderUrgent: hex(0xe86671, 1),

		Text:    hex(0xd7dae0, 1),
		TextDim: hex(0x8b93a1, 1),

		Activity: hex(0x98c379, 1),
		Red:      hex(0xe86671, 1),

		HeaderIcon: hex(0x61afef, 1),
		HeaderText: hex(0xc8cdd6, 1),

		LabelSize:      15,
		AppSize:        12,
		TitleSize:      11,
		HeaderIconSize: 34,
		HeaderSize:     30,
	}
}

// Render draws the layout onto the cairo context. The context may already be
// scaled (supersampling); all geometry is in logical pixels.
func Render(cr *cairo.Context, layout Layout, theme *Theme, opt Options) {
	r := &renderer{cr: cr, theme: theme, opt: opt, layout: pango.CairoCreateLayout(cr)}

	theme.Background.set(cr)
	cr.Paint()

	r.drawHeader(layout.Header)

	for _, panel := range layout.Panels {
		r.drawPanel(panel)
	}
}

type renderer struct {
	cr     *cairo.Context
	theme  *Theme
	opt    Options
	layout *pango.Layout
}

// lockGlyph is Font Awesome's lock (U+F023).
const lockGlyph = "\uf023"

// drawHeader centers a lock icon and "LOCK" above the panels.
func (r *renderer) drawHeader(header Rect) {
	if header.W <= 0 || header.H <= 0 {
		return
	}
	const gap = 20.0

	// Measure both runs first; r.text reuses a single pango layout.
	_, iconW, iconH := r.text(lockGlyph, r.theme.HeaderIconSize, pango.WEIGHT_NORMAL, 0)
	_, textW, textH := r.text("LOCK", r.theme.HeaderSize, pango.WEIGHT_SEMIBOLD, 0)

	x := header.X + (header.W-(iconW+gap+textW))/2
	iconY := header.Y + (header.H-iconH)/2
	textY := header.Y + (header.H-textH)/2

	r.text(lockGlyph, r.theme.HeaderIconSize, pango.WEIGHT_NORMAL, 0)
	r.drawLayout(x, iconY, iconW, iconH, r.theme.HeaderIcon, 0)

	r.text("LOCK", r.theme.HeaderSize, pango.WEIGHT_SEMIBOLD, 0)
	r.drawLayout(x+iconW+gap, textY, textW, textH, r.theme.HeaderText, 0)
}

func (r *renderer) drawPanel(panel Panel) {
	// Focus and active are deliberately not shown: on the lock screen every
	// workspace and window looks the same.
	roundedRect(r.cr, panel.Rect, 12)
	r.theme.Panel.set(r.cr)
	r.cr.Fill()

	border := r.theme.PanelBorder
	if panel.Workspace != nil && panel.Workspace.IsUrgent {
		border = r.theme.BorderUrgent
	}
	roundedRect(r.cr, panel.Rect, 12)
	border.set(r.cr)
	r.cr.SetLineWidth(1)
	r.cr.Stroke()

	r.drawPanelLabel(panel)

	if panel.Empty {
		_, w, h := r.text("empty", r.theme.LabelSize, pango.WEIGHT_NORMAL, 0)
		r.drawLayout(panel.X+(panel.W-w)/2, panel.Y+(panel.H-h)/2, w, h, r.theme.LabelDim, 0)
	}

	for _, tile := range panel.Tiles {
		r.drawTile(tile)
	}
}

func (r *renderer) drawPanelLabel(panel Panel) {
	pad := r.opt.Padding
	name := panel.Label
	colour := r.theme.Label
	if ws := panel.Workspace; ws != nil && ws.IsUrgent {
		name += "  !"
		colour = r.theme.Red
	}

	width := panel.W - 2*pad
	top := panel.Y + (r.opt.LabelHeight-r.theme.LabelSize)/2 - 2

	// Window count, right-aligned.
	count := fmt.Sprintf("%d", len(panel.Tiles))
	_, cw, _ := r.text(count, r.theme.LabelSize-2, pango.WEIGHT_NORMAL, 0)
	r.drawLayout(panel.X+panel.W-pad-cw, top+1, cw, r.theme.LabelSize, r.theme.LabelDim, 0)

	r.drawText(name, panel.X+pad, top, r.theme.LabelSize, pango.WEIGHT_SEMIBOLD, colour, width-cw-8)
}

func (r *renderer) drawTile(tile Tile) {
	if tile.W < 3 || tile.H < 3 {
		return
	}

	radius := math.Min(6, math.Min(tile.W, tile.H)/3)

	fill := r.theme.Tile
	border := r.theme.Border
	lineWidth := 1.0
	if tile.Urgent {
		fill = r.theme.TileUrgent
		border = r.theme.BorderUrgent
		lineWidth = 2
	}

	roundedRect(r.cr, tile.Rect, radius)
	fill.set(r.cr)
	r.cr.Fill()

	roundedRect(r.cr, tile.Rect, radius)
	border.set(r.cr)
	r.cr.SetLineWidth(lineWidth)
	if tile.Floating {
		r.cr.SetDash([]float64{5, 4}, 0)
	}
	r.cr.Stroke()
	r.cr.SetDash(nil, 0)

	r.drawTileText(tile)
	r.drawActivity(tile)
}

// drawActivity marks a tile whose window is working with a dot: solid while it
// is, faded while it only worked recently. Nothing else in the picture says
// what a window is doing, since focus and the active workspace are
// deliberately not drawn on a lock screen.
func (r *renderer) drawActivity(tile Tile) {
	if tile.Activity == procs.Idle || tile.W < 24 || tile.H < 24 {
		return
	}

	colour := r.theme.Activity
	if tile.Activity == procs.Warm {
		colour = colour.alpha(0.4)
	}

	r.cr.Arc(tile.X+tile.W-10, tile.Y+10, 4, 0, 2*math.Pi)
	colour.set(r.cr)
	r.cr.Fill()
}

func (r *renderer) drawTileText(tile Tile) {
	pad := math.Min(8, tile.W*0.08)
	width := tile.W - 2*pad
	if width < 24 {
		return
	}

	app := ""
	if tile.Window.AppId != nil {
		app = *tile.Window.AppId
	}
	top := tile.Y + pad
	height := r.drawText(app, tile.X+pad, top, r.theme.AppSize, pango.WEIGHT_SEMIBOLD, r.theme.Text, width)
	if !r.opt.ShowTitles {
		return
	}

	title := ""
	if tile.Window.Title != nil {
		title = *tile.Window.Title
	}
	if title == "" || title == app {
		return
	}
	top += height + 2
	if top+4 > tile.Y+tile.H-pad {
		return
	}
	r.drawText(title, tile.X+pad, top, r.theme.TitleSize, pango.WEIGHT_NORMAL, r.theme.TextDim, width)
}

// text configures the shared pango layout and returns its size in logical
// pixels. maxWidth > 0 truncates with an ellipsis.
func (r *renderer) text(s string, size float64, weight pango.Weight, maxWidth float64) (*pango.Layout, float64, float64) {
	desc := pango.FontDescriptionNew()
	desc.SetFamily(r.theme.Font)
	desc.SetWeight(weight)
	desc.SetSize(int(size * float64(pango.SCALE)))
	r.layout.SetFontDescription(desc)
	desc.Free()
	r.layout.SetWidth(-1)

	if maxWidth > 0 {
		s = r.truncate(s, maxWidth)
	}
	r.layout.SetText(s, -1)

	width, height := r.layout.GetSize()
	return r.layout, float64(width) / float64(pango.SCALE), float64(height) / float64(pango.SCALE)
}

func (r *renderer) truncate(s string, maxWidth float64) string {
	r.layout.SetText(s, -1)
	if width, _ := r.layout.GetSize(); float64(width)/float64(pango.SCALE) <= maxWidth {
		return s
	}

	runes := []rune(s)
	for n := len(runes) - 1; n > 0; n-- {
		candidate := string(runes[:n]) + "…"
		r.layout.SetText(candidate, -1)
		if width, _ := r.layout.GetSize(); float64(width)/float64(pango.SCALE) <= maxWidth {
			return candidate
		}
	}
	return "…"
}

// drawText lays out, truncates and draws a single line. clipWidth > 0 clips.
func (r *renderer) drawText(s string, x, y float64, size float64, weight pango.Weight, colour RGBA, maxWidth float64) float64 {
	if s == "" || maxWidth <= 1 {
		return 0
	}
	_, w, h := r.text(s, size, weight, maxWidth)
	r.drawLayout(x, y, w, h, colour, maxWidth)
	return h
}

func (r *renderer) drawLayout(x, y, w, h float64, colour RGBA, clipWidth float64) {
	if h <= 0 || w <= 0 {
		return
	}
	r.cr.Save()
	if clipWidth > 0 {
		r.cr.Rectangle(x, y, clipWidth, h+2)
		r.cr.Clip()
	}
	colour.set(r.cr)
	r.cr.MoveTo(x, y)
	pango.CairoShowLayout(r.cr, r.layout)
	r.cr.Restore()
}

// roundedRect builds a rounded-rectangle path. Callers then Fill or Stroke.
func roundedRect(cr *cairo.Context, rect Rect, radius float64) {
	radius = math.Max(0, math.Min(radius, math.Min(rect.W, rect.H)/2))
	cr.NewPath()
	cr.Arc(rect.X+radius, rect.Y+radius, radius, math.Pi, 1.5*math.Pi)
	cr.Arc(rect.X+rect.W-radius, rect.Y+radius, radius, 1.5*math.Pi, 2*math.Pi)
	cr.Arc(rect.X+rect.W-radius, rect.Y+rect.H-radius, radius, 0, 0.5*math.Pi)
	cr.Arc(rect.X+radius, rect.Y+rect.H-radius, radius, 0.5*math.Pi, math.Pi)
	cr.ClosePath()
}
