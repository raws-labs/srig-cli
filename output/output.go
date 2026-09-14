package output

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	Green  = lipgloss.Color("#32be78")
	yellow = lipgloss.Color("#e0a030")
	red    = lipgloss.Color("#e05252")
	Dim    = lipgloss.Color("#666666")
	white  = lipgloss.Color("#e0e0e0")

	StyleHeader = lipgloss.NewStyle().Bold(true).Foreground(white)
	StyleGreen  = lipgloss.NewStyle().Foreground(Green)
	StyleRed    = lipgloss.NewStyle().Foreground(red)
	StyleDim    = lipgloss.NewStyle().Foreground(Dim)

	stateMap = map[string]struct {
		color  lipgloss.Color
		symbol string
	}{
		"available":    {Green, "●"},
		"allocated":    {yellow, "●"},
		"offline":      {red, "●"},
		"active":       {Green, "●"},
		"idle":         {yellow, "●"},
		"pending":      {Dim, "○"},
		"allocating":   {Dim, "○"},
		"provisioning": {Dim, "○"},
		"ended":        {Dim, "○"},
	}
)

func stateIndicator(state string) string {
	if s, ok := stateMap[state]; ok {
		return lipgloss.NewStyle().Foreground(s.color).Render(s.symbol+" ") +
			lipgloss.NewStyle().Foreground(s.color).Render(state)
	}
	return state
}

// Table prints an aligned table with styled headers.
func Table(headers []string, rows [][]string) {
	if len(rows) == 0 {
		fmt.Println(StyleDim.Render("  (none)"))
		return
	}

	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	// Extra width for state indicator symbol
	stateCol := -1
	for i, h := range headers {
		if h == "STATE" {
			stateCol = i
		}
	}

	// Header
	var hdr strings.Builder
	for i, h := range headers {
		if i > 0 {
			hdr.WriteString("  ")
		}
		w := widths[i]
		if i == stateCol {
			w += 2
		}
		fmt.Fprintf(&hdr, "%-*s", w, h)
	}
	fmt.Println(StyleDim.Render("  " + hdr.String()))

	// Rows
	for _, row := range rows {
		fmt.Print("  ")
		for i, cell := range row {
			if i > 0 {
				fmt.Print("  ")
			}
			if i == stateCol {
				// stateIndicator adds the dot + color; pad the rest
				rendered := stateIndicator(cell)
				pad := widths[i] + 2 - len(cell) - 2
				if pad > 0 {
					rendered += strings.Repeat(" ", pad)
				}
				fmt.Print(rendered)
			} else {
				fmt.Printf("%-*s", widths[i], cell)
			}
		}
		fmt.Println()
	}
}

// JSON marshals v as indented JSON to stdout.
func JSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

// Error prints a styled error to stderr.
func Error(msg string) {
	fmt.Fprintln(os.Stderr, StyleRed.Render("  ✗ ")+msg)
}

// Success prints a styled success message.
func Success(msg string) {
	fmt.Println(StyleGreen.Render("  ✓ ") + msg)
}

// Info prints a styled info message.
func Info(msg string) {
	fmt.Println(StyleDim.Render("  → ") + msg)
}

// KeyValue prints a label: value pair with consistent indentation.
func KeyValue(label, value string) {
	fmt.Printf("  %s %s\n", StyleDim.Render(label+":"), value)
}

// Card prints a group of key-value pairs in a bordered box.
func Card(title string, pairs [][2]string) {
	maxLabel := 0
	for _, p := range pairs {
		if len(p[0]) > maxLabel {
			maxLabel = len(p[0])
		}
	}

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Dim).
		Padding(0, 1)

	var content strings.Builder
	if title != "" {
		content.WriteString(StyleGreen.Render(title) + "\n")
	}
	for i, p := range pairs {
		label := fmt.Sprintf("%-*s", maxLabel, p[0])
		content.WriteString(StyleDim.Render(label) + "  " + p[1])
		if i < len(pairs)-1 {
			content.WriteString("\n")
		}
	}

	fmt.Println(border.Render(content.String()))
}

// ProgressBar renders an inline progress bar. Call with \r prefix for updates.
func ProgressBar(percent float64, width int) string {
	filled := min(int(percent/100*float64(width)), width)
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	return StyleGreen.Render(bar) + StyleDim.Render(fmt.Sprintf(" %5.1f%%", percent))
}
