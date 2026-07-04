#!/usr/bin/env bash
#
# Export current tmux session to .tmuxconfig (TOML format)
# Usage: export-session.sh [output-file] [session-name]

OUTPUT_FILE="${1:-.tmuxconfig}"
SESSION="${2:-$(tmux display-message -p '#{session_name}' 2>/dev/null)}"

if [[ -z "$SESSION" ]]; then
  echo "Error: No session name provided and could not determine current session" >&2
  exit 1
fi

if ! tmux has-session -t "$SESSION" 2>/dev/null; then
  echo "Error: Session '$SESSION' not found" >&2
  exit 1
fi

# Get session root (use first window's first pane's directory as proxy)
SESSION_ROOT="$(tmux list-panes -t "$SESSION" -F "#{pane_current_path}" | head -1)"

if [[ -z "$SESSION_ROOT" ]]; then
  SESSION_ROOT="."
fi

# Temporary file for building TOML
TMPFILE=$(mktemp)
trap "rm -f '$TMPFILE'" EXIT

{
  echo "[session]"
  echo "name = \"${SESSION}\""
  echo "root = \"${SESSION_ROOT}\""
  echo ""

  # Get list of windows
  while IFS= read -r window_line; do
    [[ -z "$window_line" ]] && continue

    WINDOW_INDEX=$(echo "$window_line" | cut -d' ' -f1)
    WINDOW_NAME=$(echo "$window_line" | cut -d' ' -f2)
    WINDOW_LAYOUT=$(echo "$window_line" | cut -d' ' -f3-)

    echo "[[windows]]"
    echo "name = \"${WINDOW_NAME}\""

    if [[ -n "$WINDOW_LAYOUT" && "$WINDOW_LAYOUT" != "#{window_layout}" ]]; then
      echo "layout = \"${WINDOW_LAYOUT}\""
    fi
    echo ""

    # Get panes in this window
    while IFS= read -r pane_line; do
      [[ -z "$pane_line" ]] && continue
      echo "[[windows.panes]]"
      echo "command = \"# pane - add command\""
      echo ""
    done < <(tmux list-panes -t "$SESSION:$WINDOW_INDEX" -F "#{pane_index}" 2>/dev/null)

  done < <(tmux list-windows -t "$SESSION" -F "#{window_index} #{window_name} #{window_layout}" 2>/dev/null)

} > "$TMPFILE"

if [[ ! -s "$TMPFILE" ]]; then
  echo "Error: Failed to generate config (empty output)" >&2
  exit 1
fi

cp "$TMPFILE" "$OUTPUT_FILE"
echo "✓ Exported session '$SESSION' to $OUTPUT_FILE"
echo ""
echo "Next steps:"
echo "  1. Review the file: cat $OUTPUT_FILE"
echo "  2. Fill in the 'command' fields for each pane"
echo "  3. Test with: tmux-session -config $OUTPUT_FILE"
