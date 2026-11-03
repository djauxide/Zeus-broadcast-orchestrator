#!/usr/bin/env bash
# Splits a markdown file into files based on "File: <path>" headers.
# Fixed from the original spec version:
#   - the README uses bare "File: <path>" headers, not "### File: <path>"
#   - tolerate optional markdown hashes and trailing CR/whitespace
set -euo pipefail
DOC="${1:?usage: split-doc.sh <doc.md>}"
OUT="${2:-./zeus}"
mkdir -p "$OUT"
awk -v out="$OUT" '
  /^(#+[[:space:]]*)?File:[[:space:]]+[^[:space:]]/ {
    file = $0
    sub(/^(#+[[:space:]]*)?File:[[:space:]]+/, "", file)
    gsub(/\r$/, "", file)
    gsub(/^[ \t]+|[ \t]+$/, "", file)
    path = out "/" file
    cmd = "mkdir -p \"$(dirname " path ")\""
    system(cmd)
    capture = 1
    print "" > path
    next
  }
  capture && /^```/ {
    if (inblock) { inblock = 0; capture = 0; next }
    else          { inblock = 1; next }
  }
  capture && inblock { print > path }
' "$DOC"
echo "Materialized into $OUT"
