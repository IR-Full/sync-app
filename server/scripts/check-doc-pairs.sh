#!/usr/bin/env bash
# Every English document with a Russian twin must keep the same structure: the
# same number of headings, and in SECURITY the same defect rows. It is not a
# translation check — only a guard against one version gaining a section or a
# closed defect the other never hears about.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
status=0
for en in server/README.md server/SECURITY.md server/ARCHITECTURE.md server/GUIDE.md ios/README.md; do
  ru="${en%.md}.ru.md"
  for pattern in '^#' '^\| (✅|❌) '; do
    a=$(grep -cE "$pattern" "$root/$en" || true)
    b=$(grep -cE "$pattern" "$root/$ru" || true)
    if [ "$a" != "$b" ]; then
      echo "::error file=$ru::$en has $a lines matching /$pattern/, $ru has $b"
      status=1
    fi
  done
done
exit "$status"
