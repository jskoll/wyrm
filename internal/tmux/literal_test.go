package tmux

import "testing"

func TestLiteralText(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		// The bug: tmux's argument parser strips a trailing ";" as its command
		// separator, so this was typed as "npm run dev".
		{"npm run dev;", "npm run dev; "},
		{"SELECT 1;", "SELECT 1; "},
		// Only the last character matters — a ";" anywhere else is delivered
		// as-is and must not be disturbed.
		{"echo a; echo b", "echo a; echo b"},
		{"npm run dev", "npm run dev"},
		{"", ""},
		{";", "; "},
		// Two trailing semicolons: tmux eats only the last one, so only the
		// last one needs protecting.
		{"echo x;;", "echo x;; "},
		// A trailing backslash is left alone. Escaping the semicolon instead of
		// appending a space would have had to reason about this.
		{`echo x\`, `echo x\`},
	} {
		if got := LiteralText(tc.in); got != tc.want {
			t.Errorf("LiteralText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
