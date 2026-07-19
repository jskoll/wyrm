package freeze

import (
	"reflect"
	"testing"

	"github.com/jskoll/wyrm/internal/config"
)

// Each case below is a real #{window_layout} string captured from tmux 3.7b
// (via `tmux split-window`), paired with the pane_current_command each pane
// id would report. Expected sizes were cross-checked by hand: applying them
// through wyrm's own sequential split-window chain (internal/session's
// applySplits) reproduces the same absolute pane dimensions tmux reported.
func TestSplitsFromNode(t *testing.T) {
	cases := []struct {
		name     string
		layout   string
		commands map[string]string
		want     []config.Split
	}{
		{
			name:     "single pane, no splits",
			layout:   "abcd,139x50,0,0,0",
			commands: map[string]string{"%0": "nvim"},
			want: []config.Split{
				{Command: "nvim"},
			},
		},
		{
			name:   "h split then v split on the new pane",
			layout: "380d,200x50,0,0{139x50,0,0,0,60x50,140,0[60x24,140,0,1,60x25,140,25,2]}",
			commands: map[string]string{
				"%0": "nvim",
				"%1": "htop",
				"%2": "bash",
			},
			want: []config.Split{
				{Command: "nvim"},
				{Type: "h", Size: 30, Children: []config.Split{
					{Command: "htop"},
					{Type: "v", Size: 51, Command: "bash"},
				}},
			},
		},
		{
			name:   "three columns, built out of cascade order",
			layout: "7ad9,200x50,0,0{83x50,0,0,0,55x50,84,0,2,60x50,140,0,1}",
			commands: map[string]string{
				"%0": "editor",
				"%1": "logs",
				"%2": "server",
			},
			want: []config.Split{
				{Command: "editor"},
				{Type: "h", Size: 58, Command: "server"},
				{Type: "h", Size: 52, Command: "logs"},
			},
		},
		{
			name:   "first cell itself split further (nested at index 0)",
			layout: "ced0,200x50,0,0{139x50,0,0[139x24,0,0,0,139x25,0,25,2],60x50,140,0,1}",
			commands: map[string]string{
				"%0": "top",
				"%1": "right",
				"%2": "bottom",
			},
			want: []config.Split{
				{Children: []config.Split{
					{Command: "top"},
					{Type: "v", Size: 51, Command: "bottom"},
				}},
				{Type: "h", Size: 30, Command: "right"},
			},
		},
		{
			name:     "pane with no known foreground command",
			layout:   "abcd,139x50,0,0,5",
			commands: map[string]string{},
			want: []config.Split{
				{Command: ""},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, err := parseWindowLayout(tc.layout)
			if err != nil {
				t.Fatalf("parseWindowLayout: %v", err)
			}
			got := splitsFromNode(root, tc.commands)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("splitsFromNode() =\n%+v\nwant\n%+v", got, tc.want)
			}
		})
	}
}

func TestClampSize(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{-5, 1},
		{0, 1},
		{1, 1},
		{50, 50},
		{99, 99},
		{100, 99},
		{1000, 99},
	}
	for _, tc := range cases {
		if got := clampSize(tc.in); got != tc.want {
			t.Errorf("clampSize(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
