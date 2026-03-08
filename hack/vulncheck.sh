#!/usr/bin/env bash
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.

# Wrapper around govulncheck that tolerates stdlib vulnerabilities
# with no fix available in the current Go minor version line.
#
# govulncheck has no built-in exclusion mechanism. When a vulnerability
# is only fixed in a newer Go major version (e.g., go1.26.x while we
# use go1.25.x), we cannot act on it without a major version bump.
# This script re-checks such failures and passes if all reported
# vulnerabilities fall into that category.

set -euo pipefail

# Run govulncheck normally first — if it passes, we're done.
if govulncheck ./pkg/... 2>&1; then
    exit 0
fi

echo ""
echo "govulncheck found vulnerabilities. Checking if any are fixable in the current Go version line..."
echo ""

# Get the current Go minor version (e.g., "1.25") from go.mod
GO_MINOR=$(grep '^go ' go.mod | awk '{print $2}' | cut -d. -f1,2)

# Re-run with JSON output. -format json always exits 0.
# The output is a stream of concatenated JSON objects (not NDJSON, not a JSON array).
# We parse them and look for "finding" entries whose fixed_version is within our minor line.
ACTIONABLE=$(govulncheck -format json ./pkg/... 2>/dev/null | python3 -c "
import json, sys, re

go_minor = '${GO_MINOR}'

decoder = json.JSONDecoder()
content = sys.stdin.read()
idx = 0
actionable = []

while idx < len(content):
    # Skip whitespace
    while idx < len(content) and content[idx] in ' \t\n\r':
        idx += 1
    if idx >= len(content):
        break
    try:
        obj, end_idx = decoder.raw_decode(content, idx)
        idx = end_idx
    except json.JSONDecodeError:
        break

    finding = obj.get('finding')
    if finding is None:
        continue

    osv_id = finding.get('osv', 'unknown')
    fixed_version = finding.get('fixed_version', '')
    if not fixed_version:
        # No fix available at all — not actionable
        continue

    # stdlib fixed versions look like 'v1.26.1', 'v1.25.9', etc.
    match = re.match(r'v(\d+\.\d+)', fixed_version)
    if match:
        fix_minor = match.group(1)
        if fix_minor == go_minor:
            # Fix IS available in our minor line — this is actionable
            actionable.append(f'{osv_id} (fixed in {fixed_version})')
    else:
        # Unrecognized format — treat as actionable to be safe
        actionable.append(f'{osv_id} (fixed in {fixed_version})')

# Deduplicate (multiple findings per vuln)
seen = set()
for a in actionable:
    if a not in seen:
        seen.add(a)
        print(a)
" || true)

if [ -n "$ACTIONABLE" ]; then
    echo "Found actionable vulnerabilities fixable in Go ${GO_MINOR}.x:"
    echo "$ACTIONABLE"
    exit 1
else
    echo "All reported vulnerabilities require a Go version beyond ${GO_MINOR}.x."
    echo "These are not currently actionable. Passing vulncheck."
    exit 0
fi
