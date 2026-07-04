#!/usr/bin/env bash
#
# Export current tmux session to .tmuxconfig (TOML format)
# Usage: export-session.sh [output-file] [session-name]

set -euo pipefail

OUTPUT_FILE="${1:-.tmuxconfig}"
SESSION="${2:=$(tmux display-message -p '#{session_name}')}"

if ! tmux has-session -t "$SESSION" 2>/dev/null; then
  echo "Error: Session '$SESSION' not found" >&2
  exit 1
fi

# Get session root (use current pane's directory as proxy for session root)
SESSION_ROOT="$(tmux display-message -t "$SESSION" -p '#{pane_current_path}')"

# Helper function to get pane command
get_pane_command() {
  local session=$1 window=$2 pane=$3
  local pane_id="$session:$window.$pane"

  # Try to get the command from pane's process
  # Fall back to shell prompt indicator
  tmux send-keys -t "$pane_id" "echo '# pane content'" C-c 2>/dev/null || true

  # For now, return empty — user will fill in commands they want
  # (we can't reliably extract running commands without side effects)
  echo ""
}

# Get window layout
get_window_layout() {
  local session=$1 window=$2
  tmux list-windows -t "$session" -F "#{window_index} #{window_layout}" | grep "^$window " | cut -d' ' -f2-
}

# Build TOML
{
  echo "[session]"
  echo "name = \"${SESSION}\""
  echo "root = \"${SESSION_ROOT}\""
  echo ""

  # Iterate windows
  WINDOW_COUNT=$(tmux list-windows -t "$SESSION" -F "#{window_index}" | wc -l)
  for ((w = 0; w < WINDOW_COUNT; w++)); do
    WINDOW_NAME=$(tmux list-windows -t "$SESSION" -F "#{window_index} #{window_name}" | awk -v w="$w" '$1==w {print $2}')
    LAYOUT=$(get_window_layout "$SESSION" "$w")

    echo "[[windows]]"
    echo "name = \"${WINDOW_NAME}\""
    if [[ -n "$LAYOUT" ]]; then
      echo "layout = \"${LAYOUT}\""
    fi
    echo ""

    # Get pane count for this window
    PANE_COUNT=$(tmux list-panes -t "$SESSION:$w" -F "#{pane_index}" | wc -l)
    for ((p = 0; p < PANE_COUNT; p++)); do
      echo "[[windows.panes]]"
      echo "command = \"# pane $p - fill in command\""
      echo ""
    done
  done
} > "$OUTPUT_FILE"

echo "Exported session '$SESSION' to $OUTPUT_FILE"
echo ""
echo "Review and fill in command fields:"
grep -n "# pane" "$OUTPUT_FILE" || true
