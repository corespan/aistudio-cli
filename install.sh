#!/bin/bash
# =============================================================================
# ai-studio-cli installer
# =============================================================================
#
# Usage:
#   ./install.sh                 # install the latest release
#   ./install.sh v1.2.3          # install a specific version
#   VERSION=v1.2.3 ./install.sh  # same, via environment
#
# What changed and why
# --------------------
# The previous version resolved "latest" at run time, downloaded a tarball over
# plain curl, and `sudo mv`d the contents into /usr/local/bin with no
# verification of any kind. Three separate problems:
#
#   Unpinned    Two people running the same command a week apart got different
#               binaries, and neither could say which. A script that installs
#               "whatever is newest" cannot be used in a reproducible
#               provisioning flow, which is what this tool is for.
#
#   Unverified  No checksum, no signature. Anything that could tamper with the
#               download — a compromised release asset, a proxy, a redirect —
#               ended up as an unverified binary in /usr/local/bin, with sudo.
#
#   Untidy      Extracted into /tmp with fixed names, so two concurrent runs
#               clobbered each other, and a failure left files behind.
#
# It now pins a version (defaulting to latest, resolved once and reported),
# verifies a published SHA256SUMS, uses a private temp directory, and says what
# it is installing before asking for privilege.
# =============================================================================

set -euo pipefail

REPO="corespan/aistudio-cli"
BINARY_NAME="ai-studio-cli"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
VERSION="${1:-${VERSION:-}}"

die() { echo "ERROR: $*" >&2; exit 1; }

for cmd in curl tar sha256sum; do
    command -v "$cmd" >/dev/null 2>&1 || die "'$cmd' is required but not installed."
done

# ── Resolve the version ───────────────────────────────────────────────────────
if [ -z "$VERSION" ]; then
    echo "Resolving latest release..."
    VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
        | grep '"tag_name"' | head -1 | sed -E 's/.*"([^"]+)".*/\1/')
    [ -n "$VERSION" ] || die "could not resolve the latest release tag."
    echo "Latest release is ${VERSION}."
    echo "For a reproducible install, pin it:  ./install.sh ${VERSION}"
    echo
fi

BASE="https://github.com/${REPO}/releases/download/${VERSION}"
TARBALL="${VERSION}.tar.gz"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "Downloading ${TARBALL} ..."
curl -fsSL "${BASE}/${TARBALL}" -o "${TMP}/${TARBALL}" \
    || die "download failed. Does release ${VERSION} exist?"

# ── Verify ────────────────────────────────────────────────────────────────────
# The missing-checksum branch is a warning only while older releases predate
# SHA256SUMS. Once every supported release publishes one, make it a hard
# failure — an unverifiable binary going into /usr/local/bin under sudo
# deserves to stop the install.
echo "Verifying checksum ..."
if curl -fsSL "${BASE}/SHA256SUMS" -o "${TMP}/SHA256SUMS" 2>/dev/null; then
    ( cd "$TMP" && grep " ${TARBALL}\$" SHA256SUMS | sha256sum -c - ) \
        || die "CHECKSUM MISMATCH for ${TARBALL}.

The download does not match the published checksum. Do not install it.
Report this at https://github.com/${REPO}/issues"
    echo "  Checksum OK."

    if curl -fsSL "${BASE}/SHA256SUMS.asc" -o "${TMP}/SHA256SUMS.asc" 2>/dev/null; then
        if command -v gpg >/dev/null 2>&1 \
           && gpg --verify "${TMP}/SHA256SUMS.asc" "${TMP}/SHA256SUMS" 2>/dev/null; then
            echo "  Signature OK."
        else
            echo "  NOTE: signature present but not verified (signing key not in your keyring)."
        fi
    fi
else
    echo "  WARNING: no SHA256SUMS published for ${VERSION} — cannot verify this download."
    echo "           Continuing; this release predates checksum publishing."
fi

# ── Extract ───────────────────────────────────────────────────────────────────
tar -xzf "${TMP}/${TARBALL}" -C "$TMP"
[ -f "${TMP}/${BINARY_NAME}" ] || die "the tarball does not contain ${BINARY_NAME}."
chmod +x "${TMP}/${BINARY_NAME}"

# ── Install ───────────────────────────────────────────────────────────────────
echo
echo "Installing ${BINARY_NAME} ${VERSION} to ${INSTALL_DIR}/"
if [ -w "$INSTALL_DIR" ]; then
    mv "${TMP}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
else
    echo "  ${INSTALL_DIR} is not writable — using sudo."
    sudo mv "${TMP}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
fi

echo
echo "Done. Installed ${BINARY_NAME} ${VERSION}."
echo "  Run:               ${BINARY_NAME} --help"
echo "  Licence notices:   ${BINARY_NAME} licenses"
