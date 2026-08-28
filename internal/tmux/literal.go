package tmux

import "strings"

// LiteralText prepares text for `send-keys -l -- <text>`, working around
// tmux's own argument parser eating a trailing semicolon.
//
// tmux treats ";" as its command separator, and an argument whose *last*
// character is ";" has it stripped before send-keys ever sees the string —
// `-l --` protects against key-name lookup and flag interpretation, but not
// against this. So a pane command of "npm run dev;" was typed as "npm run dev".
//
// In a shell that is invisible, since a trailing ";" is a no-op there. It stops
// being invisible the moment the pane is running something else: in psql,
// sqlite3, or any REPL where ";" terminates a statement, dropping it means the
// statement is typed and then simply never executed.
//
// The fix is a trailing space. tmux delivers "cmd; " byte for byte, and the
// space is inert everywhere the semicolon mattered — shells and REPLs alike
// ignore trailing whitespace before the newline. The alternative, escaping the
// final ";" as "\;", is also honoured by tmux, but only at the very end of the
// argument, and it interacts badly with text the user ended in a backslash;
// appending a space needs no such reasoning.
func LiteralText(text string) string {
	if strings.HasSuffix(text, ";") {
		return text + " "
	}
	return text
}
