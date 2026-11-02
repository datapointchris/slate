package cli

import (
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/datapointchris/gotermstyle"
	"golang.org/x/term"
)

// The table cells here are padded by hand rather than through text/tabwriter,
// because tabwriter counts an escape sequence as visible width and one colored
// cell shifts every column after it. Each helper measures the plain string in
// runes and colors it afterwards, so the pad is never dyed.

func padRight(s string, width int) string {
	if n := utf8.RuneCountInString(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

func padLeft(s string, width int) string {
	if n := utf8.RuneCountInString(s); n < width {
		return strings.Repeat(" ", width-n) + s
	}
	return s
}

func widest(values []string) int {
	width := 0
	for _, v := range values {
		width = max(width, utf8.RuneCountInString(v))
	}
	return width
}

func paint(color func(string) string, text string, width int) string {
	return color(text) + strings.Repeat(" ", max(width-utf8.RuneCountInString(text), 0))
}

// tierCode is the color a priority word and its group header take. Only the
// two tiers that ask for attention get a hue; medium is bright, and low and none
// stay in the terminal's own foreground.
func tierCode(priority int) string {
	switch priority {
	case 1:
		return gotermstyle.CodeRed
	case 2:
		return gotermstyle.CodeYellow
	case 3:
		return gotermstyle.CodeWhite
	default:
		return ""
	}
}

func tierColor(p gotermstyle.Palette, priority int) func(string) string {
	code := tierCode(priority)
	return func(text string) string { return p.Paint(code, text) }
}

// clipsToTerminal reports whether rows written to out are clipped to the
// terminal. Piped and redirected output is read whole by whatever reads it.
func clipsToTerminal(out io.Writer) bool {
	file, ok := out.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}
