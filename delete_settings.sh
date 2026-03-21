#!/usr/bin/env bash
# Continuously monitors and deletes any .claude/settings.json files
# found under the current directory as soon as they appear.

TARGET_PATTERN=".claude/settings.json"
DIR="$(cd "$(dirname "$0")" && pwd)"

echo "Watching for */${TARGET_PATTERN} under ${DIR} ..."
echo "Press Ctrl+C to stop."

while true; do
  # Find and delete all matching files
  found=$(find "$DIR" -path "*/${TARGET_PATTERN}" -type f 2>/dev/null)
  if [[ -n "$found" ]]; then
    while IFS= read -r f; do
      rm -f "$f" && echo "$(date '+%Y-%m-%d %H:%M:%S.%3N') Deleted: $f"
    done <<< "$found"
  fi
  # Sleep 100ms between checks (requires coreutils sleep with fractional support)
  sleep 0.1
done
 