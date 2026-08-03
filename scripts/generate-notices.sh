#!/usr/bin/env bash
# =============================================================================
# Generate the third-party licence notices compiled into the binary
# =============================================================================
#
# WHY
#
# A Go binary statically links every dependency. Publishing a release binary
# distributes compiled copies of cobra, viper, pflag, fsnotify and the rest.
# MIT, BSD-3 and Apache-2.0 all condition redistribution on carrying the
# copyright notice, and there is no node_modules or site-packages beside the
# artifact for those notices to live in — the binary IS the distribution.
#
# So the notices go inside it, via go:embed, and `ai-studio-cli licenses`
# prints them. Same approach as kubectl, docker and gh, and the only one that
# survives someone copying the binary to a machine with no network.
#
# Output: ai-studio-cli/internal/notices/THIRD-PARTY-NOTICES.txt (committed)
#
# Usage:
#   ./scripts/generate-notices.sh            # regenerate
#   ./scripts/generate-notices.sh --check    # CI: fail if stale
#
# Requires: Go toolchain and network access to proxy.golang.org.
# =============================================================================

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODULE_DIR="$ROOT/ai-studio-cli"
OUT="$MODULE_DIR/internal/notices/THIRD-PARTY-NOTICES.txt"
UI_NOTICE="$MODULE_DIR/internal/benchui/ui/vendor/NOTICE"

CHECK=0
[ "${1:-}" = "--check" ] && CHECK=1

command -v go >/dev/null 2>&1 || { echo "ERROR: Go toolchain not found." >&2; exit 1; }

# go-licenses resolves the real build graph — only what is actually linked into
# the binary, not everything in go.sum. That distinction matters: go.sum lists
# modules pulled in for tests and tooling that never reach a user.
if ! command -v go-licenses >/dev/null 2>&1; then
  echo "Installing go-licenses ..."
  go install github.com/google/go-licenses@latest
  export PATH="$PATH:$(go env GOPATH)/bin"
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

cd "$MODULE_DIR"

echo "Resolving licences for the linked module graph ..."
# save writes each dependency's licence file into a directory tree; csv gives
# the module -> licence mapping for the summary table.
go-licenses save ./... --save_path="$WORK/licences" --force 2>"$WORK/save.err" || {
  echo "ERROR: go-licenses save failed:" >&2
  cat "$WORK/save.err" >&2
  exit 1
}
go-licenses csv ./... > "$WORK/licences.csv" 2>"$WORK/csv.err" || {
  echo "ERROR: go-licenses csv failed:" >&2
  cat "$WORK/csv.err" >&2
  exit 1
}

# Anything here is a decision, not routine attribution. Strong copyleft in a
# statically linked binary we hand to customers is a materially different
# question from MIT.
FLAG='GPL|AGPL|LGPL|MPL|SSPL|BUSL|CC-BY-NC|Commons-Clause|Unknown|unknown'

{
  echo "ai-studio-cli — third-party licence notices"
  printf '=%.0s' {1..72}; echo
  echo
  echo "This binary statically links the Go modules listed below, and embeds the"
  echo "web UI assets noted at the end. Distributing the binary distributes copies"
  echo "of all of it, so their licences are reproduced here in full."
  echo
  echo "CoreSpan AI's own source is Apache-2.0 and is NOT covered by these notices."
  echo "See LICENSE and NOTICE at https://github.com/corespan/aistudio-cli"
  echo
  echo "Generated: $(date -u +%Y-%m-%d) by scripts/generate-notices.sh"
  echo
  printf -- '-%.0s' {1..72}; echo
  echo
  echo "SUMMARY"
  echo
  # Skip the CSV header if go-licenses emits one, and align for readability.
  awk -F, 'NF>=3 && $1!="module" { printf "  %-58s %s\n", $1, $3 }' "$WORK/licences.csv" | sort -u
  echo
  echo "  Modules: $(awk -F, 'NF>=3 && $1!="module"' "$WORK/licences.csv" | wc -l)"
  echo

  if awk -F, 'NF>=3' "$WORK/licences.csv" | grep -qE "$FLAG"; then
    echo "  REQUIRES REVIEW — copyleft or unresolved licences present:"
    awk -F, 'NF>=3' "$WORK/licences.csv" | grep -E "$FLAG" | sed 's/^/    /'
    echo
  else
    echo "  No copyleft or unresolved licences. Attribution obligations only."
    echo
  fi

  printf -- '-%.0s' {1..72}; echo
  echo

  # Full text of every licence file go-licenses collected.
  find "$WORK/licences" -type f \( -iname 'LICENSE*' -o -iname 'LICENCE*' -o -iname 'COPYING*' -o -iname 'NOTICE*' \) \
    | sort \
    | while read -r f; do
        module="${f#"$WORK/licences/"}"
        module="$(dirname "$module")"
        echo "$module"
        echo
        sed 's/^/    /' "$f"
        echo
        printf -- '-%.0s' {1..72}; echo
        echo
      done

  # The UI assets are embedded too, and go-licenses cannot see them — they are
  # not Go modules. Append their notices so one command covers the whole binary.
  if [ -f "$UI_NOTICE" ]; then
    echo "EMBEDDED WEB UI ASSETS"
    echo
    sed 's/^/    /' "$UI_NOTICE"
    echo
    printf -- '-%.0s' {1..72}; echo
    echo
    for lf in "$MODULE_DIR"/internal/benchui/ui/vendor/fonts/LICENSE-*.txt \
              "$MODULE_DIR"/internal/benchui/ui/vendor/js/LICENSE-*.md; do
      [ -f "$lf" ] || continue
      echo "$(basename "$lf")"
      echo
      sed 's/^/    /' "$lf"
      echo
      printf -- '-%.0s' {1..72}; echo
      echo
    done
  else
    echo "WARNING: $UI_NOTICE not found — run ./scripts/vendor-ui-assets.sh" >&2
  fi
} | sed 's/[[:space:]]*$//' > "$WORK/notices.txt"

if [ "$CHECK" -eq 1 ]; then
  if [ ! -f "$OUT" ]; then
    echo "ERROR: $OUT is missing. Run 'make notices'." >&2
    exit 1
  fi
  # Ignore the Generated: date, which changes every run.
  if ! diff <(grep -v '^Generated:' "$OUT") <(grep -v '^Generated:' "$WORK/notices.txt"); then
    echo >&2
    echo "ERROR: embedded notices are out of date. Run 'make notices' and commit." >&2
    exit 1
  fi
  echo "Embedded notices are up to date."
  exit 0
fi

cp "$WORK/notices.txt" "$OUT"
echo "Wrote $OUT ($(wc -c < "$OUT") bytes)"
grep -q "NOTICES-NOT-GENERATED" "$OUT" && {
  echo "ERROR: placeholder marker survived generation — this should not happen." >&2
  exit 1
}
echo "Verify with: cd ai-studio-cli && go run . licenses | head"
