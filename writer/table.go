package writer

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/dineshba/tf-summarize/terraformstate"
	"github.com/olekukonko/tablewriter"
)

// TableWriter writes resource changes in a table format.
type TableWriter struct {
	mdEnabled     bool
	details       bool
	changes       map[string]terraformstate.ResourceChanges
	outputChanges map[string][]string
}

var tableOrder = []string{"import", "add", "update", "recreate", "delete", "moved"}

func (t TableWriter) Write(writer io.Writer) error {
	if t.details {
		return t.writeDetails(writer)
	}
	return t.writeStandard(writer)
}

// writeStandard is the original tablewriter-based rendering (no -details).
func (t TableWriter) writeStandard(writer io.Writer) error {
	tableString := make([][]string, 0, 4)

	for _, change := range tableOrder {
		changedResources := t.changes[change]
		resourceCount := len(changedResources)
		for _, changedResource := range changedResources {
			changeLabel := fmt.Sprintf("%s (%d)", change, resourceCount)
			if change == "moved" {
				if t.mdEnabled {
					tableString = append(tableString, []string{changeLabel, fmt.Sprintf("`%s` to `%s`", changedResource.PreviousAddress, changedResource.Address)})
				} else {
					tableString = append(tableString, []string{changeLabel, fmt.Sprintf("%s to %s", changedResource.PreviousAddress, changedResource.Address)})
				}
			} else {
				if t.mdEnabled {
					tableString = append(tableString, []string{changeLabel, fmt.Sprintf("`%s`", changedResource.Address)})
				} else {
					tableString = append(tableString, []string{changeLabel, changedResource.Address})
				}
			}
		}
	}

	table := tablewriter.NewWriter(writer)
	table.SetHeader([]string{"Change", "Resource"})
	table.SetAutoMergeCells(true)
	table.SetAutoWrapText(false)
	table.SetRowLine(true)
	table.AppendBulk(tableString)

	if t.mdEnabled {
		table.SetBorders(tablewriter.Border{Left: true, Top: false, Right: true, Bottom: false})
		table.SetCenterSeparator("|")
	}

	table.Render()

	if hasOutputChanges(t.outputChanges) {
		tableString = make([][]string, 0, 4)
		for _, change := range tableOrder {
			changedOutputs := t.outputChanges[change]
			outputCount := len(changedOutputs)
			for _, changedOutput := range changedOutputs {
				if t.mdEnabled {
					tableString = append(tableString, []string{fmt.Sprintf("%s (%d)", change, outputCount), fmt.Sprintf("`%s`", changedOutput)})
				} else {
					tableString = append(tableString, []string{fmt.Sprintf("%s (%d)", change, outputCount), changedOutput})
				}
			}
		}
		table = tablewriter.NewWriter(writer)
		table.SetHeader([]string{"Change", "Output"})
		table.SetAutoMergeCells(true)
		table.SetAutoWrapText(false)
		table.SetRowLine(true)
		table.AppendBulk(tableString)

		if t.mdEnabled {
			_, _ = fmt.Fprint(writer, tablewriter.NEWLINE)
			table.SetBorders(tablewriter.Border{Left: true, Top: false, Right: true, Bottom: false})
			table.SetCenterSeparator("|")
		}

		table.Render()
	}

	return nil
}

// resourceBlock holds the rendered lines for a single resource entry.
type resourceBlock struct {
	change      string   // change type label (first resource in group) or "" (subsequent)
	changeColor string   // ANSI color for this group
	lines       []string // [0]=address, [1:]=attribute diff lines (plain key: value strings)
	lastInGroup bool
}

// writeDetails renders the details table with full ANSI styling:
//   - Header row/border: bold, no color
//   - All borders/pipes/separators from first === onward: group color
//   - Change label: color + bold
//   - Resource address: color + bold
//   - Attribute keys: bold only
//   - Attribute values: plain
//   - Heavy === between change groups, light --- between resources in group
func (t TableWriter) writeDetails(writer io.Writer) error {
	blocks := make([]resourceBlock, 0)

	for _, change := range tableOrder {
		changedResources := t.changes[change]
		if len(changedResources) == 0 {
			continue
		}

		color := terraformstate.ChangeColor(change)
		isCreate := change == "add" || change == "import"
		isDelete := change == "delete"

		for i, rc := range changedResources {
			var addr string
			if change == "moved" {
				addr = fmt.Sprintf("%s to %s", rc.PreviousAddress, rc.Address)
			} else {
				addr = rc.Address
			}

			// lines[0] = address (plain — styled at render time)
			// lines[1:] = "  key: value" or "  key: before -> after" (plain)
			lines := []string{addr}

			diffs := terraformstate.GetAttributeDiffs(rc)
			for _, d := range diffs {
				switch {
				case isCreate:
					lines = append(lines, fmt.Sprintf("  %s: %s", d.Key, d.After))
				case isDelete:
					lines = append(lines, fmt.Sprintf("  %s: %s", d.Key, d.Before))
				default:
					lines = append(lines, fmt.Sprintf("  %s: %s -> %s", d.Key, d.Before, d.After))
				}
			}

			label := ""
			if i == 0 {
				label = change
			}

			blocks = append(blocks, resourceBlock{
				change:      label,
				changeColor: color,
				lines:       lines,
				lastInGroup: i == len(changedResources)-1,
			})
		}
	}

	if len(blocks) == 0 {
		return nil
	}

	// Column widths — measured on plain text so ANSI codes don't skew padding
	col1W := utf8.RuneCountInString("CHANGE")
	col2W := utf8.RuneCountInString("RESOURCE")
	for _, b := range blocks {
		if w := utf8.RuneCountInString(b.change); w > col1W {
			col1W = w
		}
		for _, line := range b.lines {
			// Measure only the visible part: for attr lines strip the leading "  key: "
			// but we measure the whole line for column width purposes since it all goes in col2.
			if w := utf8.RuneCountInString(line); w > col2W {
				col2W = w
			}
		}
	}

	p := func(s string) { fmt.Fprintln(writer, s) }

	// hLine builds a full-width horizontal rule.
	// fill is "-" or "=". color optionally tints the fill segments and the pipes.
	hLine := func(fill, color string) string {
		pipe := "+"
		seg1 := strings.Repeat(fill, col1W+2)
		seg2 := strings.Repeat(fill, col2W+2)
		if color != "" {
			pipe = color + "+" + terraformstate.ColorReset
			seg1 = color + seg1 + terraformstate.ColorReset
			seg2 = color + seg2 + terraformstate.ColorReset
		}
		return pipe + seg1 + pipe + seg2 + pipe
	}

	// dataRow builds a data row with colored pipes.
	// c1: left cell content (plain), c2: right cell content (plain)
	// c1Styled: pre-styled version of c1 (with ANSI), c2Styled same for c2.
	// pipeColor: color for | characters; "" = default.
	dataRow := func(c1Plain, c1Styled, c2Plain, c2Styled, pipeColor string) string {
		pad1 := col1W - utf8.RuneCountInString(c1Plain)
		pad2 := col2W - utf8.RuneCountInString(c2Plain)
		pipe := "|"
		if pipeColor != "" {
			pipe = pipeColor + "|" + terraformstate.ColorReset
		}
		return fmt.Sprintf("%s %s%s %s %s%s %s",
			pipe,
			c1Styled, strings.Repeat(" ", pad1),
			pipe,
			c2Styled, strings.Repeat(" ", pad2),
			pipe,
		)
	}

	// styleAttrLine applies bold to the key portion of an attribute line.
	// Input format: "  key: rest" or "  key: before -> after"
	// Returns (plainKey+rest for width, styledLine for display).
	styleAttrLine := func(line string) string {
		// line starts with "  " then key: value
		trimmed := strings.TrimPrefix(line, "  ")
		colonIdx := strings.Index(trimmed, ": ")
		if colonIdx < 0 {
			return line
		}
		key := trimmed[:colonIdx]
		rest := trimmed[colonIdx:] // ": value"
		return "  " + bold(key) + rest
	}

	// ── Header (bold, no color) ──────────────────────────────────────────────
	p(hLine("-", ""))
	p(dataRow("CHANGE", bold("CHANGE"), "RESOURCE", bold("RESOURCE"), ""))
	// First heavy separator — no color yet (transition into first group color below)
	p(hLine("=", ""))

	currentColor := ""

	for bi, b := range blocks {
		// On the first row of each group, update the active color
		if b.change != "" {
			currentColor = b.changeColor
		}

		// ── Address row ──────────────────────────────────────────────────────
		c1Plain := b.change
		c1Styled := colorBold(b.change, currentColor) // colored+bold, or "" if blank
		c2Plain := b.lines[0]
		c2Styled := colorBold(b.lines[0], currentColor) // resource address colored+bold
		p(dataRow(c1Plain, c1Styled, c2Plain, c2Styled, currentColor))

		// ── Attribute lines ──────────────────────────────────────────────────
		for _, dl := range b.lines[1:] {
			styledDl := styleAttrLine(dl)
			p(dataRow("", "", dl, styledDl, currentColor))
		}

		// ── Separator after this block ───────────────────────────────────────
		isLast := bi == len(blocks)-1
		if isLast {
			p(hLine("-", currentColor))
		} else if b.lastInGroup {
			// Heavy separator; tint with the *next* group's color
			nextColor := blocks[bi+1].changeColor
			p(hLine("=", nextColor))
			currentColor = nextColor
		} else {
			// Light separator within the same group
			p(hLine("-", currentColor))
		}
	}

	return nil
}

// NewTableWriter returns a new TableWriter.
func NewTableWriter(changes map[string]terraformstate.ResourceChanges, outputChanges map[string][]string, mdEnabled bool, details bool) Writer {
	return TableWriter{
		changes:       changes,
		mdEnabled:     mdEnabled,
		details:       details,
		outputChanges: outputChanges,
	}
}
