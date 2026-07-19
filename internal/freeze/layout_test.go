package freeze

import "testing"

func TestParseWindowLayoutLeaf(t *testing.T) {
	n, err := parseWindowLayout("e112,139x50,0,0,0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n.Type != 0 {
		t.Fatalf("Type = %q, want leaf (0)", n.Type)
	}
	if n.PaneID != "%0" {
		t.Errorf("PaneID = %q, want %%0", n.PaneID)
	}
	if n.W != 139 || n.H != 50 {
		t.Errorf("dims = %dx%d, want 139x50", n.W, n.H)
	}
}

func TestParseWindowLayoutContainer(t *testing.T) {
	// Captured from a real tmux 3.7 session: one -h split (30%) then, on the
	// new right-hand pane, a -v split (50%).
	n, err := parseWindowLayout("380d,200x50,0,0{139x50,0,0,0,60x50,140,0[60x24,140,0,1,60x25,140,25,2]}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n.Type != '{' {
		t.Fatalf("root Type = %q, want '{'", n.Type)
	}
	if len(n.Children) != 2 {
		t.Fatalf("root has %d children, want 2", len(n.Children))
	}
	if n.Children[0].PaneID != "%0" {
		t.Errorf("first child pane = %q, want %%0", n.Children[0].PaneID)
	}
	sub := n.Children[1]
	if sub.Type != '[' {
		t.Fatalf("second child Type = %q, want '['", sub.Type)
	}
	if len(sub.Children) != 2 || sub.Children[0].PaneID != "%1" || sub.Children[1].PaneID != "%2" {
		t.Fatalf("nested container = %+v, want two leaves %%1 then %%2", sub.Children)
	}
}

func TestParseWindowLayoutErrors(t *testing.T) {
	cases := []string{
		"",
		"nochecksum",
		"e112,",
		"e112,139x50,0,0,",                  // trailing comma, no pane id digits
		"e112,139x50,0,0{60x50,0,0,0}extra", // trailing garbage after close
		"e112,139x50,0,0{60x50,0,0,0",       // missing closing brace
		"e112,139x50,0",                     // missing Y
		"e112,139y50,0,0,0",                 // 'x' replaced
	}
	for _, in := range cases {
		if _, err := parseWindowLayout(in); err == nil {
			t.Errorf("parseWindowLayout(%q) succeeded, want an error", in)
		}
	}
}
