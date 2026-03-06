package writer

import (
	"fmt"
	"io"

	"github.com/dineshba/tf-summarize/terraformstate"
	"github.com/dineshba/tf-summarize/tree"
)

// TreeWriter writes resource changes in a tree format.
type TreeWriter struct {
	changes  terraformstate.ResourceChanges
	drawable bool
	details  bool
}

func (t TreeWriter) Write(writer io.Writer) error {
	trees := tree.CreateTree(t.changes)

	if t.drawable {
		drawableTree := trees.DrawableTree()
		_, err := fmt.Fprint(writer, drawableTree.String())
		return err
	}

	for _, tr := range trees {
		err := printTree(writer, tr, "", t.details)
		if err != nil {
			return fmt.Errorf("error writing data to %s: %s", writer, err.Error())
		}
	}
	return nil
}

// NewTreeWriter returns a new TreeWriter.
func NewTreeWriter(changes terraformstate.ResourceChanges, drawable bool, details bool) Writer {
	return TreeWriter{changes: changes, drawable: drawable, details: details}
}

func printTree(writer io.Writer, t *tree.Tree, prefixSpace string, details bool) error {
	var err error
	prefixSymbol := fmt.Sprintf("%s|---", prefixSpace)
	if t.Value != nil {
		colorPrefix, suffix := terraformstate.GetColorPrefixAndSuffixText(t.Value)
		_, err = fmt.Fprintf(writer, "%s%s%s%s%s\n", prefixSymbol, colorPrefix, t.Name, suffix, terraformstate.ColorReset)
		if err != nil {
			return fmt.Errorf("error writing data to %s: %s", writer, err.Error())
		}
		if details {
			diffs := terraformstate.GetAttributeDiffs(t.Value)
			detailPrefix := fmt.Sprintf("%s|\t  ", prefixSpace)
			isCreate := t.Value.Change.Actions.Create() && !t.Value.Change.Actions.Delete()
			isDelete := t.Value.Change.Actions.Delete() && !t.Value.Change.Actions.Create()
			for _, d := range diffs {
				switch {
				case isCreate:
					_, err = fmt.Fprintf(writer, "%s%s: %s\n", detailPrefix, d.Key, d.After)
				case isDelete:
					_, err = fmt.Fprintf(writer, "%s%s: %s\n", detailPrefix, d.Key, d.Before)
				default:
					_, err = fmt.Fprintf(writer, "%s%s: %s -> %s\n", detailPrefix, d.Key, d.Before, d.After)
				}
				if err != nil {
					return fmt.Errorf("error writing data to %s: %s", writer, err.Error())
				}
			}
		}
	} else {
		_, err = fmt.Fprintf(writer, "%s%s\n", prefixSymbol, t.Name)
		if err != nil {
			return fmt.Errorf("error writing data to %s: %s", writer, err.Error())
		}
	}

	for _, c := range t.Children {
		separator := "|"
		err = printTree(writer, c, fmt.Sprintf("%s%s\t", prefixSpace, separator), details)
		if err != nil {
			return fmt.Errorf("error writing data to %s: %s", writer, err.Error())
		}
	}
	return nil
}
