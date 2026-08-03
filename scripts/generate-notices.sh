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
# HOW — deliberately without go-licenses
#
# The first version of this script shelled out to `go-licenses save`. That added
# a `go install ...@latest` to every CI run (unpinned, and a network dependency
# beyond the module proxy) and go-licenses is known to fail hard on modules
# whose licence it cannot classify — which turns a licence-notice job into a
# licence-classifier argument.
#
# `go list -deps` already answers the only question that matters: which modules
# are actually linked into this binary. The module cache already contains their
# licence files. Reading them directly needs no extra tooling, cannot fail on an
# unrecognised licence, and is a shorter path to the thing the obligation
# actually requires — the verbatim text.
#
# We do not assert SPDX identifiers we cannot derive. The heading for each
# module names the licence FILE as shipped; the text below it governs.
#
# Output: ai-studio-cli/internal/notices/THIRD-PARTY-NOTICES.txt
#
# Usage:  ./scripts/generate-notices.sh
# Requires: Go toolchain, and network access on first run to populate the
#           module cache (`go mod download`).
# =============================================================================

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODULE_DIR="$ROOT/ai-studio-cli"
OUT="$MODULE_DIR/internal/notices/THIRD-PARTY-NOTICES.txt"
UI_NOTICE="$MODULE_DIR/internal/benchui/ui/vendor/NOTICE"

command -v go >/dev/null 2>&1 || { echo "ERROR: Go toolchain not found." >&2; exit 1; }

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

cd "$MODULE_DIR"

echo "Resolving the linked module graph ..."
# NOT preceded by `go mod download`.
#
# `go mod download` with no arguments resolves the whole build list, which can
# include modules that go.sum has no entry for because they are not needed to
# compile anything. It then fails with "missing go.sum entry" — a job that
# passes `go build` and `go vet` and fails here, which is exactly what happened
# on the first run of this workflow.
#
# `go list -deps` downloads precisely what it needs to resolve the imports of
# the main package, which is the set we want anyway.
#
# -deps walks everything the main package transitively imports — i.e. what ends
# up in the binary. Modules only; the standard library has no .Module and is
# covered by the Go LICENSE, handled separately below.
#
# Run into a file with an explicit status check rather than as the head of a
# pipeline: `set -o pipefail` would surface a go failure as an opaque exit, and
# `sed`/`sort` would happily produce an empty file from an error.
if ! go list -deps \
      -f '{{if .Module}}{{.Module.Path}}|{{.Module.Version}}|{{.Module.Dir}}{{end}}' . \
      > "$WORK/raw.txt" 2> "$WORK/list.err"; then
  echo "ERROR: 'go list -deps' failed:" >&2
  cat "$WORK/list.err" >&2
  exit 1
fi

# `sed '/^$/d'` rather than `grep -v '^$'`: grep exits 1 when it filters
# everything out, which under pipefail would fail the pipeline.
sed '/^$/d' "$WORK/raw.txt" | sort -u > "$WORK/modules.txt"

# Drop our own module — its licence is LICENSE/NOTICE in the repo root.
SELF="$(go list -m)"
grep -v "^${SELF}|" "$WORK/modules.txt" > "$WORK/deps.txt" || true

COUNT=$(wc -l < "$WORK/deps.txt")
[ "$COUNT" -gt 0 ] || { echo "ERROR: resolved no dependencies — refusing to write empty notices." >&2; exit 1; }
echo "  $COUNT modules linked into the binary."

# A module resolved but not present on disk yields an empty .Module.Dir, and
# every licence file for it would then be silently absent. Fail loudly instead:
# quietly attributing nothing is the failure this whole script exists to prevent.
NODIR=$(awk -F'|' '$3 == "" { print "  " $1 "@" $2 }' "$WORK/deps.txt")
if [ -n "$NODIR" ]; then
  echo "ERROR: these modules resolved with no on-disk directory:" >&2
  echo "$NODIR" >&2
  echo "Their licence files cannot be read. Try 'go mod download <module>'." >&2
  exit 1
fi

# Licence filenames as they appear in the wild.
find_licence_files() {
  local dir="$1"
  [ -d "$dir" ] || return 0
  find "$dir" -maxdepth 1 -type f \
    \( -iname 'LICENSE*' -o -iname 'LICENCE*' -o -iname 'COPYING*' -o -iname 'NOTICE*' \) \
    2>/dev/null | sort
}

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
  echo "Go: $(go version | awk '{print $3}')"
  echo
  printf -- '-%.0s' {1..72}; echo
  echo
  echo "MODULES ($COUNT)"
  echo
  while IFS='|' read -r path version dir; do
    [ -n "$path" ] || continue
    printf '  %-56s %s\n' "$path" "$version"
  done < "$WORK/deps.txt"
  echo
  printf -- '-%.0s' {1..72}; echo
  echo

  MISSING=""
  while IFS='|' read -r path version dir; do
    [ -n "$path" ] || continue
    echo "$path@$version"
    echo

    files=$(find_licence_files "$dir")
    if [ -z "$files" ]; then
      # Recorded rather than skipped. A module with no licence file in its
      # distribution is something to notice, not to quietly omit.
      echo "    No licence file was shipped in this module's distribution."
      echo "    See https://$path for its terms."
      MISSING="$MISSING $path"
    else
      while read -r f; do
        [ -n "$f" ] || continue
        echo "    [$(basename "$f")]"
        echo
        sed 's/^/    /' "$f"
        echo
      done <<< "$files"
    fi
    printf -- '-%.0s' {1..72}; echo
    echo
  done < "$WORK/deps.txt"

  # The Go standard library is linked into every Go binary and carries its own
  # BSD-3 licence. go list reports no .Module for it, so it needs naming here.
  GOROOT_LIC="$(go env GOROOT)/LICENSE"
  if [ -f "$GOROOT_LIC" ]; then
    echo "The Go standard library"
    echo
    echo "    [LICENSE]"
    echo
    sed 's/^/    /' "$GOROOT_LIC"
    echo
    printf -- '-%.0s' {1..72}; echo
    echo
  fi

  # The go:embed'd UI assets are not Go modules, so nothing above sees them.
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

  # if/then, not `[ -n ... ] && echo`. This is the last command in the block,
  # and with `set -o pipefail` a false test here makes the whole pipeline fail —
  # so the script would abort precisely when nothing was wrong.
  if [ -n "$MISSING" ]; then
    echo "Modules shipping no licence file:$MISSING" >&2
  fi
} | sed 's/[[:space:]]*$//' > "$WORK/notices.txt"

# Never overwrite good notices with something degenerate.
if grep -q "NOTICES-NOT-GENERATED" "$WORK/notices.txt"; then
  echo "ERROR: generated output still contains the placeholder marker." >&2
  exit 1
fi
LINES=$(wc -l < "$WORK/notices.txt")
if [ "$LINES" -lt 50 ]; then
  echo "ERROR: generated output is only $LINES lines — refusing to write it." >&2
  exit 1
fi

cp "$WORK/notices.txt" "$OUT"
echo
echo "Wrote $OUT"
echo "  $(wc -c < "$OUT") bytes, $LINES lines, $(grep -ci copyright "$OUT") copyright lines"
echo "Verify with: cd $MODULE_DIR && go run . licenses | head -40"
