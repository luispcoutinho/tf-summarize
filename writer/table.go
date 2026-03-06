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
	changeColor string   // ANSI color for the change label and separators
	lines       []string // address line + attribute diff lines
	lastInGroup bool
}

// writeDetails renders the table with a custom renderer that supports:
//   - ANSI color on change labels and group separators (option A)
//   - Filtered, prefix-free attribute display for creates (option C)
//   - Heavy === separators between change groups
//   - Light --- separators between resources within a group
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
			// Resource address line
			var addr string
			if change == "moved" {
				addr = fmt.Sprintf("%s to %s", rc.PreviousAddress, rc.Address)
			} else {
				addr = rc.Address
			}

			lines := []string{addr}

			// Attribute diff lines
			diffs := terraformstate.GetAttributeDiffs(rc)
			for _, d := range diffs {
				switch {
				case isCreate:
					// Option C: no "(none) ->" prefix, just the value
					lines = append(lines, fmt.Sprintf("  %s: %s", d.Key, d.After))
				case isDelete:
					// Delete: just the id value, no arrow
					lines = append(lines, fmt.Sprintf("  %s: %s", d.Key, d.Before))
				default:
					// Update / recreate: before -> after
					lines = append(lines, fmt.Sprintf("  %s: %s -> %s", d.Key, d.Before, d.After))
				}
			}

			// Change label only on first resource in the group
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

	// Calculate column widths (based on plain text, ignoring ANSI codes)
	col1W := len("CHANGE")
	col2W := len("RESOURCE")
	for _, b := range blocks {
		if w := utf8.RuneCountInString(b.change); w > col1W {
			col1W = w
		}
		for _, line := range b.lines {
			if w := utf8.RuneCountInString(line); w > col2W {
				col2W = w
			}
		}
	}

	// Rendering helpers
	//
	// hLine builds a horizontal rule. colorStr wraps the fill characters in ANSI
	// color so the separator itself is tinted — pass "" for uncolored lines.
	hLine := func(left, mid, right, fill, colorStr string) string {
		seg1 := strings.Repeat(fill, col1W+2)
		seg2 := strings.Repeat(fill, col2W+2)
		if colorStr != "" {
			seg1 = colorStr + seg1 + terraformstate.ColorReset
			seg2 = colorStr + seg2 + terraformstate.ColorReset
		}
		return left + seg1 + mid + seg2 + right
	}

	// row builds a table row. c1Color optionally colors the first cell content.
	row := func(c1, c2, c1Color string) string {
		pad1 := col1W - utf8.RuneCountInString(c1)
		pad2 := col2W - utf8.RuneCountInString(c2)
		cell1 := c1 + strings.Repeat(" ", pad1)
		if c1Color != "" && c1 != "" {
			cell1 = c1Color + c1 + terraformstate.ColorReset + strings.Repeat(" ", pad1)
		}
		return fmt.Sprintf("| %s | %s%s |", cell1, c2, strings.Repeat(" ", pad2))
	}

	p := func(s string) { fmt.Fprintln(writer, s) }

	topLine  := hLine("+", "+", "+", "-", "")
	botLine  := hLine("+", "+", "+", "-", "")
	lightLine := hLine("+", "+", "+", "-", "") // resource boundary within group (no color)

	// Header
	p(topLine)
	p(row("CHANGE", "RESOURCE", ""))
	// Header separator uses no color
	p(hLine("+", "+", "+", "=", ""))

	for bi, b := range blocks {
		// Address row — color the change label if this is the first in the group
		p(row(b.change, b.lines[0], b.changeColor))
		// Attribute detail lines — change col blank, no color on detail text
		for _, dl := range b.lines[1:] {
			p(row("", dl, ""))
		}

		isLast := bi == len(blocks)-1
		if isLast {
			p(botLine)
		} else if b.lastInGroup {
			// Heavy colored separator at change group boundary
			// Use the color of the *next* block's change type for the incoming group
			nextColor := blocks[bi+1].changeColor
			p(hLine("+", "+", "+", "=", nextColor))
		} else {
			p(lightLine) // light separator between resources in same group
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
