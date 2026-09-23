// Command niri-overview renders an all-workspaces minimap of a niri session.
//
// M0 renders one frame to a PNG, meant to be used as a swaylock background:
//
//	niri-overview --png /tmp/lock.png && swaylock -f -i /tmp/lock.png
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/gotk3/gotk3/cairo"
	"wnw/niri"
	"wnw/overview"
)

func main() {
	pngPath := flag.String("png", "", "render one frame to PATH and exit (required)")
	size := flag.String("size", "", "canvas size WxH (default: the output's logical size)")
	outputName := flag.String("output", "", "output to render (default: the focused output)")
	cols := flag.Int("cols", 0, "panel columns (0 stacks the workspaces vertically)")
	scale := flag.Float64("scale", 1, "supersampling factor, e.g. 2 for crisp HiDPI output")
	contentWidth := flag.Float64("content-width", 0.60, "fraction of the canvas width the centered panel column may use")
	panelAspect := flag.Float64("panel-aspect", 1.2, "width/height ratio of a single workspace panel")
	noTitles := flag.Bool("no-titles", false, "do not draw window titles")
	showEmpty := flag.Bool("show-empty", false, "also draw workspaces without any window")
	flag.Parse()

	if err := run(*pngPath, *size, *outputName, *cols, *scale, *contentWidth, *panelAspect, !*noTitles, *showEmpty); err != nil {
		fmt.Fprintf(os.Stderr, "niri-overview: %s\n", err)
		os.Exit(1)
	}
}

func run(pngPath, size, outputName string, cols int, scale, contentWidth, panelAspect float64, titles, showEmpty bool) error {
	if pngPath == "" {
		return fmt.Errorf("only --png is implemented so far (M0); live mode is M1")
	}
	if scale <= 0 {
		return fmt.Errorf("--scale must be positive")
	}

	snapshot, err := niri.QuerySnapshot()
	if err != nil {
		return err
	}
	output, ok := snapshot.Output(outputName)
	if !ok {
		return fmt.Errorf("no outputs")
	}

	width, height := output.Size()
	if size != "" {
		width, height, err = parseSize(size)
		if err != nil {
			return fmt.Errorf("--size: %w", err)
		}
	}
	if width <= 0 || height <= 0 {
		return fmt.Errorf("output %q has no usable size; pass --size WxH", output.Name)
	}

	views := viewsForOutput(snapshot.Views, output.Name)

	opt := overview.DefaultOptions(float64(width), float64(height), float64(width), float64(height))
	opt.Cols = cols
	opt.ShowTitles = titles
	opt.ShowEmpty = showEmpty
	if contentWidth > 0 {
		opt.ContentWidthFraction = contentWidth
	}
	if panelAspect > 0 {
		opt.PanelAspect = panelAspect
	}

	layout := overview.Build(views, opt)

	pixelWidth := int(math.Round(opt.Width * scale))
	pixelHeight := int(math.Round(opt.Height * scale))
	surface := cairo.CreateImageSurface(cairo.FORMAT_ARGB32, pixelWidth, pixelHeight)
	context := cairo.Create(surface)
	if scale != 1 {
		context.Scale(scale, scale)
	}
	overview.Render(context, layout, overview.DefaultTheme(), opt)
	context.Close()

	surface.Flush()
	if err := surface.WriteToPNG(pngPath); err != nil {
		return fmt.Errorf("writing %s: %w", pngPath, err)
	}
	return nil
}

func viewsForOutput(views []niri.WorkspaceView, output string) []niri.WorkspaceView {
	if output == "" {
		return views
	}
	filtered := make([]niri.WorkspaceView, 0, len(views))
	for _, view := range views {
		if view.Workspace.Output != nil && *view.Workspace.Output == output {
			filtered = append(filtered, view)
		}
	}
	return filtered
}

func parseSize(value string) (int, int, error) {
	parts := strings.SplitN(strings.ToLower(value), "x", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("expected WxH, got %q", value)
	}
	width, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("bad width: %w", err)
	}
	height, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("bad height: %w", err)
	}
	return width, height, nil
}
