package tui

// This file owns where things are on screen. Both View and the mouse hit test
// read the layout from here rather than each working it out for itself: a click
// that selects a different row than the one under the pointer is the signature
// bug of hit testing that re-derives the geometry, and the only way to rule it
// out is to have one description of the layout and two readers of it.

// panelBox is one list panel's outer rectangle in the left column. Rows are
// [y0, y1); x spans [0, leftW).
type panelBox struct {
	y0, y1 int
}

// listRows returns the row range the panel's list entries occupy: inside the
// top border and the title row, and stopping before the bottom border.
func (b panelBox) listRows() (y0, y1 int) {
	top := b.y0 + 1 + titleRows // top border, then the title
	bottom := b.y1 - 1          // bottom border
	if bottom < top {
		bottom = top
	}
	return top, bottom
}

// geometry is the frame's layout for one render: the column split and where
// each of the four panels sits.
type geometry struct {
	leftW, rightW int
	bodyH         int
	heights       []int
	boxes         [numPanels]panelBox
}

// geometry computes the current layout from the model's size and list lengths.
// It is a pure function of state that Update can also see, which is what lets
// the hit test agree with the last frame drawn.
func (m Model) geometry() geometry {
	leftW := m.width * 30 / 100
	if leftW < minLeftWidth {
		leftW = minLeftWidth
	}
	if leftW > maxLeftWidth {
		leftW = maxLeftWidth
	}

	g := geometry{
		leftW:  leftW,
		rightW: m.width - leftW,
		bodyH:  m.height - helpHeight,
	}
	g.heights = panelHeights(g.bodyH, []int{
		m.panelLen(panelProjects), m.panelLen(panelSessions),
		m.panelLen(panelWindows), m.panelLen(panelPanes),
	})
	y := 0
	for i := range g.boxes {
		g.boxes[i] = panelBox{y0: y, y1: y + g.heights[i]}
		y += g.heights[i]
	}
	return g
}

// hit is what the pointer is over.
type hit struct {
	panel panel
	// row is the index of the list entry under the pointer, or -1 when the
	// pointer is over the panel's chrome (border, title, or empty space below
	// the last entry). Callers that only want to change focus can use the panel
	// regardless; callers that move a selection must check row.
	row int
}

// hitTest reports what is at screen cell (x, y). It returns false for anything
// outside the four list panels — the preview pane and the footer included.
func (m Model) hitTest(x, y int) (hit, bool) {
	if !m.ready || m.width < minWidth || m.height < minHeight {
		return hit{}, false
	}
	g := m.geometry()
	if x < 0 || x >= g.leftW || y < 0 || y >= g.bodyH {
		return hit{}, false
	}
	for p := panel(0); p < numPanels; p++ {
		box := g.boxes[p]
		if y < box.y0 || y >= box.y1 {
			continue
		}
		return hit{panel: p, row: m.rowAt(p, box, y)}, true
	}
	return hit{}, false
}

// rowAt maps a screen row to an index into the panel's visible list, or -1 if
// the row holds chrome or trailing blank space. It walks the same viewport the
// renderer scrolled to, so a scrolled panel maps correctly.
func (m Model) rowAt(p panel, box panelBox, y int) int {
	top, bottom := box.listRows()
	if y < top || y >= bottom {
		return -1
	}
	n := m.panelLen(p)
	if n == 0 {
		return -1
	}
	listH := bottom - top
	start, _ := viewport(m.cursorFor(p), n, listH)
	idx := start + (y - top)
	if idx >= n {
		return -1
	}
	return idx
}

// cursorFor is the current selection index of any panel — the panel-addressed
// counterpart of focusedCursor.
func (m Model) cursorFor(p panel) int {
	switch p {
	case panelProjects:
		return m.projectCur
	case panelSessions:
		return m.sessionCur
	case panelWindows:
		return m.windowCur
	case panelPanes:
		return m.paneCur
	}
	return 0
}
