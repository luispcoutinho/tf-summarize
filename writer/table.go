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
	change   string // change type label (first resource in group) or "" (subsequent)
	lines    []string
	lastInGroup bool
}

// writeDetails renders the table with a custom renderer that supports
// per-resource dividers (light) and per-change-group dividers (heavy).
func (t TableWriter) writeDetails(writer io.Writer) error {
	// Build all resource blocks
	blocks := make([]resourceBlock, 0)

	for _, change := range tableOrder {
		changedResources := t.changes[change]
		if len(changedResources) == 0 {
			continue
		}

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
			isDelete := rc.Change.Actions.Delete() && !rc.Change.Actions.Create()
			for _, d := range diffs {
				if isDelete {
					lines = append(lines, fmt.Sprintf("  %s: %s", d.Key, d.Before))
				} else {
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
				lines:       lines,
				lastInGroup: i == len(changedResources)-1,
			})
		}
	}

	if len(blocks) == 0 {
		return nil
	}

	// Calculate column widths
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
	hLine := func(left, mid, right, fill string) string {
		return left + strings.Repeat(fill, col1W+2) + mid + strings.Repeat(fill, col2W+2) + right
	}
	row := func(c1, c2 string) string {
		pad1 := col1W - utf8.RuneCountInString(c1)
		pad2 := col2W - utf8.RuneCountInString(c2)
		return fmt.Sprintf("| %s%s | %s%s |", c1, strings.Repeat(" ", pad1), c2, strings.Repeat(" ", pad2))
	}

	heavyLine := hLine("+", "+", "+", "=") // between change groups
	lightLine := hLine("+", "+", "+", "-") // between resources in a group
	topLine   := hLine("+", "+", "+", "-") // top border
	botLine   := hLine("+", "+", "+", "-") // bottom border

	p := func(s string) {
		fmt.Fprintln(writer, s)
	}

	// Header
	p(topLine)
	p(row("CHANGE", "RESOURCE"))
	p(heavyLine)

	for bi, b := range blocks {
		// First line of the resource block (address)
		p(row(b.change, b.lines[0]))
		// Detail lines — change col is always blank
		for _, dl := range b.lines[1:] {
			p(row("", dl))
		}

		isLast := bi == len(blocks)-1
		if isLast {
			p(botLine)
		} else if b.lastInGroup {
			p(heavyLine) // change group boundary
		} else {
			p(lightLine) // resource boundary within same group
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
