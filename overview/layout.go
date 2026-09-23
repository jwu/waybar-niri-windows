// Package overview computes and renders an all-workspaces minimap of a niri
// session. niri's own IPC only describes the layout in relative terms, so the
// renderer reconstructs each workspace from its windows' tile sizes.
package overview

import (
	"math"
	"slices"
	"strconv"
	"wnw/niri"
)

// Rect is a rectangle in canvas pixels (logical, before any supersampling).
type Rect struct {
	X, Y, W, H float64
}

// Tile is one window drawn inside a workspace panel.
type Tile struct {
	Rect
	Window *niri.Window
	Urgent bool
	// Floating is drawn with a dashed border.
	Floating bool
	// Busy is set by the live view when the window's title changed recently.
	Busy bool
}

// Panel is one workspace (its background plus its windows).
type Panel struct {
	Rect
	Workspace *niri.Workspace
	Label     string
	Tiles     []Tile
	Empty     bool
}

// Layout is the complete picture to render.
type Layout struct {
	Width, Height float64
	// Header is the area above the panels, centered horizontally.
	Header Rect
	Panels []Panel
}

// Options controls the geometry of a layout.
type Options struct {
	// Canvas size in logical pixels.
	Width, Height float64
	// Logical size of the output, used to place floating windows.
	ViewWidth, ViewHeight float64
	// Margin around the whole canvas, padding inside a panel, and the gap
	// between panels/windows/tiles.
	Margin, Padding, Gap float64
	// Panel columns; <= 0 stacks the workspaces vertically.
	Cols int
	// Height reserved at the top of each panel for its label.
	LabelHeight float64
	// Whether to render window titles (app ids are always shown).
	ShowTitles bool
	// Height reserved above the panels for the lock header.
	HeaderHeight float64
	// Fraction of the canvas width the panel column may use. Panels are
	// centered, so the overview stays a compact block in the middle.
	ContentWidthFraction float64
	// Width/height ratio of a single panel. Bigger means shorter panels.
	PanelAspect float64
	// Whether workspaces without any window are rendered too.
	ShowEmpty bool
}

// DefaultOptions returns sensible geometry for the given canvas and output size.
func DefaultOptions(width, height, viewWidth, viewHeight float64) Options {
	return Options{
		Width:                width,
		Height:               height,
		ViewWidth:            viewWidth,
		ViewHeight:           viewHeight,
		Margin:               24,
		Padding:              16,
		Gap:                  10,
		Cols:                 1,
		LabelHeight:          36,
		ShowTitles:           true,
		HeaderHeight:         96,
		ContentWidthFraction: 0.60,
		PanelAspect:          1.2,
	}
}

// Build lays out every non-empty workspace into a centered, vertically stacked
// column of panels.
func Build(views []niri.WorkspaceView, opt Options) Layout {
	layout := Layout{Width: opt.Width, Height: opt.Height}

	panels := make([]niri.WorkspaceView, 0, len(views))
	for _, view := range views {
		if !opt.ShowEmpty && len(view.Tiled) == 0 && len(view.Floating) == 0 {
			continue
		}
		panels = append(panels, view)
	}

	contentWidth := math.Min(opt.Width-2*opt.Margin, opt.Width*opt.ContentWidthFraction)
	contentX := (opt.Width - contentWidth) / 2

	cols := opt.Cols
	if cols <= 0 {
		cols = 1
	}
	n := len(panels)
	cols = clampInt(cols, 1, max(n, 1))
	rows := 0
	if n > 0 {
		rows = (n + cols - 1) / cols
	}

	panelWidth := (contentWidth - opt.Gap*float64(cols-1)) / float64(cols)
	panelHeight := panelWidth / opt.PanelAspect

	// Shrink the panels if the column would not fit on the canvas.
	if rows > 0 {
		available := opt.Height - 2*opt.Margin - opt.HeaderHeight - opt.Gap*float64(rows-1)
		if panelHeight*float64(rows) > available {
			panelHeight = available / float64(rows)
		}
		panelHeight = math.Max(panelHeight, 80)
	}

	totalHeight := opt.HeaderHeight
	if rows > 0 {
		totalHeight += opt.Gap*float64(rows-1) + panelHeight*float64(rows)
	}
	top := math.Max(opt.Margin, (opt.Height-totalHeight)/2)

	layout.Header = Rect{X: contentX, Y: top, W: contentWidth, H: opt.HeaderHeight}

	for i, view := range panels {
		row, col := i/cols, i%cols
		panel := Panel{
			Rect: Rect{
				X: contentX + float64(col)*(panelWidth+opt.Gap),
				Y: top + opt.HeaderHeight + float64(row)*(panelHeight+opt.Gap),
				W: panelWidth,
				H: panelHeight,
			},
			Workspace: view.Workspace,
			Label:     workspaceLabel(view.Workspace),
		}

		content := Rect{
			X: panel.X + opt.Padding,
			Y: panel.Y + opt.LabelHeight + opt.Padding,
			W: panel.W - 2*opt.Padding,
			H: panel.H - opt.LabelHeight - 2*opt.Padding,
		}
		panel.Tiles = buildTiles(view, content, opt)
		panel.Empty = len(panel.Tiles) == 0
		layout.Panels = append(layout.Panels, panel)
	}

	return layout
}

type column struct {
	windows []*niri.Window
	// Natural (unscaled) column width and total height, in niri logical pixels.
	width  float64
	height float64
}

func buildTiles(view niri.WorkspaceView, content Rect, opt Options) []Tile {
	columns := groupColumns(view.Tiled)

	var totalWidth, maxHeight float64
	for _, col := range columns {
		totalWidth += col.width
		maxHeight = math.Max(maxHeight, col.height)
	}

	// No tiling layout: show floating windows side by side.
	if len(columns) == 0 || totalWidth <= 0 || maxHeight <= 0 {
		return floatingRow(view.Floating, content, opt)
	}

	// Fit the reconstructed layout into the panel, preserving aspect ratio.
	hGaps := opt.Gap * float64(len(columns)-1)
	scale := math.Min((content.W-hGaps)/totalWidth, content.H/maxHeight)
	canvasW := totalWidth * scale
	canvasH := maxHeight * scale
	originX := content.X + (content.W-canvasW)/2
	originY := content.Y + (content.H-canvasH)/2

	var tiles []Tile
	x := originX
	for _, col := range columns {
		width := col.width * scale
		// Distribute the column's height between its windows proportionally to
		// their natural height.
		avail := canvasH - opt.Gap*float64(len(col.windows)-1)
		y := originY
		for _, win := range col.windows {
			height := win.Layout.TileSize.Y / col.height * avail
			tiles = append(tiles, newTile(Rect{x, y, width, height}, win))
			y += height + opt.Gap
		}
		x += width + opt.Gap
	}

	for _, win := range view.Floating {
		tiles = append(tiles, floatingTile(win, Rect{originX, originY, canvasW, canvasH}, content, opt))
	}

	return tiles
}

func newTile(rect Rect, win *niri.Window) Tile {
	return Tile{
		Rect:   rect,
		Window: win,
		Urgent: win.IsUrgent,
	}
}

// floatingTile places a floating window using its position in the workspace
// view, scaled to the reconstructed canvas.
func floatingTile(win *niri.Window, canvas, content Rect, opt Options) Tile {
	tile := newTile(Rect{W: canvas.W * 0.35, H: canvas.H * 0.25}, win)
	tile.Floating = true

	if pos := win.Layout.TilePosInWorkspaceView; pos != nil && opt.ViewWidth > 0 && opt.ViewHeight > 0 {
		tile.X = canvas.X + pos.X/opt.ViewWidth*canvas.W
		tile.Y = canvas.Y + pos.Y/opt.ViewHeight*canvas.H
		tile.W = win.Layout.TileSize.X / opt.ViewWidth * canvas.W
		tile.H = win.Layout.TileSize.Y / opt.ViewHeight * canvas.H
	} else {
		tile.X = canvas.X + (canvas.W-tile.W)/2
		tile.Y = canvas.Y + (canvas.H-tile.H)/2
	}

	// Keep the tile inside the panel's content box.
	tile.W = math.Max(tile.W, 16)
	tile.H = math.Max(tile.H, 12)
	tile.X = math.Max(tile.X, content.X)
	tile.Y = math.Max(tile.Y, content.Y)
	tile.W = math.Min(tile.W, content.X+content.W-tile.X)
	tile.H = math.Min(tile.H, content.Y+content.H-tile.Y)
	return tile
}

// floatingRow lays out floating windows side by side when there is no tiling
// layout to position them over.
func floatingRow(windows []*niri.Window, content Rect, opt Options) []Tile {
	if len(windows) == 0 {
		return nil
	}
	width := (content.W - opt.Gap*float64(len(windows)-1)) / float64(len(windows))
	tiles := make([]Tile, 0, len(windows))
	for i, win := range windows {
		rect := Rect{
			X: content.X + float64(i)*(width+opt.Gap),
			Y: content.Y,
			W: width,
			H: content.H,
		}
		tile := newTile(rect, win)
		tile.Floating = true
		tiles = append(tiles, tile)
	}
	return tiles
}

// groupColumns groups tiled windows by their column index and measures each
// column. Tiles in a column share a width; their heights add up to the column
// height.
func groupColumns(windows []*niri.Window) []column {
	byX := make(map[uint32]*column)
	var order []uint32
	for _, win := range windows {
		var x uint32
		if win.Layout.PosInScrollingLayout != nil {
			x = win.Layout.PosInScrollingLayout.X
		}
		col, ok := byX[x]
		if !ok {
			col = &column{}
			byX[x] = col
			order = append(order, x)
		}
		col.windows = append(col.windows, win)
		col.width = math.Max(col.width, win.Layout.TileSize.X)
		col.height += win.Layout.TileSize.Y
	}

	slices.Sort(order)
	columns := make([]column, 0, len(order))
	for _, x := range order {
		columns = append(columns, *byX[x])
	}
	return columns
}

func workspaceLabel(ws *niri.Workspace) string {
	if ws == nil {
		return "?"
	}
	if ws.Name != nil && *ws.Name != "" {
		return *ws.Name
	}
	return strconv.Itoa(int(ws.Index))
}

func clampInt(v, lo, hi int) int {
	return min(max(v, lo), hi)
}
