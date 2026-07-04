#!/usr/bin/env bash
#
# Export current tmux session to .tmuxconfig (TOML format)
# Usage: export-session.sh [output-file] [session-name]

# Get current pane's working directory (safer than relying on script's cwd)
PANE_DIR="$(tmux display-message -p '#{pane_current_path}' 2>/dev/null)" || PANE_DIR="."

# Use pane directory if output file is relative path
if [[ "${1:-.tmuxconfig}" != /* ]]; then
  OUTPUT_FILE="$PANE_DIR/${1:-.tmuxconfig}"
else
  OUTPUT_FILE="${1:-.tmuxconfig}"
fi

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
SESSION_ROOT="$(tmux list-panes -t "$SESSION" -F "#{pane_current_path}" 2>/dev/null | head -1)"

if [[ -z "$SESSION_ROOT" ]]; then
  SESSION_ROOT="."
fi

# Build TOML directly (avoid subshell issues)
{
  echo "[session]"
  echo "name = \"${SESSION}\""
  echo "root = \"${SESSION_ROOT}\""
  echo ""

  # Get windows
  tmux list-windows -t "$SESSION" -F "#{window_index}|#{window_name}|#{window_layout}" 2>/dev/null | while IFS='|' read -r w_idx w_name w_layout; do
    [[ -z "$w_idx" ]] && continue

    echo "[[windows]]"
    echo "name = \"${w_name}\""

    # Only include layout if it looks valid (not the format string)
    if [[ -n "$w_layout" && "$w_layout" != "#{window_layout}" ]]; then
      echo "layout = \"${w_layout}\""
    fi
    echo ""

    # Get panes in this window
    tmux list-panes -t "$SESSION:$w_idx" -F "#{pane_index}" 2>/dev/null | while IFS= read -r p_idx; do
      [[ -z "$p_idx" ]] && continue
      echo "[[windows.panes]]"
      echo "command = \"# pane - fill in command\""
      echo ""
    done
  done

} > "$OUTPUT_FILE"

if [[ ! -s "$OUTPUT_FILE" ]]; then
  echo "Error: Failed to generate config (empty output)" >&2
  rm -f "$OUTPUT_FILE"
  exit 1
fi

echo "✓ Exported session '$SESSION' to .tmuxconfig"
echo "  Full path: $OUTPUT_FILE"
echo ""
echo "Next: Edit the file and fill in command fields"
echo "  cat $OUTPUT_FILE"
