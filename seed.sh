#!/bin/bash
# Bulk-import a folder of self-contained HTML cards into a Quartzite store.
#
# Usage: ./seed.sh <cards-dir> [store-dir]
#   cards-dir  a folder of *.html files, each one becoming a folder document
#   store-dir  the store root; defaults to $QUARTZITE_STORE
#
# Useful when agent-authored HTML already exists somewhere — stranded,
# unlinked, unsearchable — and you want it in the graph.

set -euo pipefail

CARDS="${1:-}"
DEST="${2:-${QUARTZITE_STORE:-}}"

if [ -z "$CARDS" ] || [ ! -d "$CARDS" ]; then
  echo "seed: usage: $0 <cards-dir> [store-dir]  (no cards directory at '${CARDS:-<unset>}')" >&2
  exit 1
fi
if [ -z "$DEST" ]; then
  echo "seed: no store given — pass a store dir or set QUARTZITE_STORE" >&2
  exit 1
fi
DEST="$DEST/artifacts"

mkdir -p "$DEST"

count=0
for f in "$CARDS"/*.html; do
  [ -e "$f" ] || continue
  base="$(basename "$f" .html)"

  # Each card becomes a folder document so it can gain assets later without
  # changing its identity or its URL.
  mkdir -p "$DEST/$base"
  cp "$f" "$DEST/$base/index.html"

  # Title-case the slug for a readable title.
  title="$(echo "$base" | tr '-' ' ' | awk '{for(i=1;i<=NF;i++) $i=toupper(substr($i,1,1)) substr($i,2)}1')"

  cat > "$DEST/$base/meta.json" <<JSON
{
  "title": "$title",
  "tags": ["artifact", "imported"],
  "description": "Self-contained HTML card imported by seed.sh.",
  "entry": "index.html"
}
JSON
  count=$((count + 1))
done

echo "seed: imported $count artifacts into $DEST"
