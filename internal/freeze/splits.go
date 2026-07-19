package freeze

import (
	"github.com/jskoll/wyrm/internal/config"
)

// splitsFromNode converts a parsed layout node into the []config.Split tree
// wyrm's own split builder (internal/session.applySplits) would need to
// reproduce it.
//
// The two models don't match shape for shape: tmux's layout is a container
// of sibling cells with absolute dimensions, while wyrm splits sequentially
// — each entry with a type splits off the *previous* entry's newly created
// pane, not the container's original space (see applySplits). But they are
// equivalent: repeatedly splitting the most recently created pane produces
// exactly the same left-to-right (or top-to-bottom) sequence of cells tmux
// reports in a container, provided each entry's Size is recovered relative
// to the shrinking pane being split at that step rather than the
// container's total. That's what the loop below does: at step i it recovers
// the percentage handed to cell i-1 out of whatever remained after cells
// 0..i-2 were carved off, then reduces "remaining" by cell i-1's share
// before moving on.
func splitsFromNode(n *layoutNode, commands map[string]string) []config.Split {
	if n.Type == 0 {
		return []config.Split{{Command: commands[n.PaneID]}}
	}

	splitType := "h"
	dim := func(c *layoutNode) int { return c.W }
	if n.Type == '[' {
		splitType = "v"
		dim = func(c *layoutNode) int { return c.H }
	}

	dims := make([]int, len(n.Children))
	total := 0
	for i, c := range n.Children {
		dims[i] = dim(c)
		total += dims[i]
	}

	splits := make([]config.Split, len(n.Children))
	remaining := total
	for i, c := range n.Children {
		var entry config.Split
		if i > 0 {
			entry.Type = splitType
			if remaining > 0 {
				// Round to nearest rather than truncate: (num + den/2) / den.
				numerator := 100*(remaining-dims[i-1]) + remaining/2
				entry.Size = clampSize(numerator / remaining)
			}
			remaining -= dims[i-1]
		}
		if c.Type == 0 {
			entry.Command = commands[c.PaneID]
		} else {
			entry.Children = splitsFromNode(c, commands)
		}
		splits[i] = entry
	}
	return splits
}

// clampSize keeps a rounded percentage inside the 1-99 range config.Split
// validates: rounding can otherwise push a near-empty or near-total cell to
// 0 or 100, and 100 fails validation outright.
func clampSize(percent int) int {
	if percent < 1 {
		return 1
	}
	if percent > 99 {
		return 99
	}
	return percent
}
