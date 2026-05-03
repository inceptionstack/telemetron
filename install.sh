#!/usr/bin/env sh
# SPDX-License-Identifier: Apache-2.0
#
# telemetron installer — downloads the latest GitHub Release tarball for
# the current OS/arch, verifies its SHA-256 against the published
# checksums.txt, and installs the binary into a user-writable bin dir.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/inceptionstack/telemetron/main/install.sh | sh
#
# Optional environment overrides:
#   TELEMETRON_VERSION   pin a specific release tag (default: latest)
#   TELEMETRON_PREFIX    install root (default: $HOME/.local, or /usr/local with sudo)
#
# The script is POSIX shell and uses only curl, tar, sha256sum|shasum, uname, sed.
# It does NOT install the systemd service; that requires root. After install,
# run `telemetron install --help` for service setup.

set -eu

REPO="inceptionstack/telemetron"
VERSION="${TELEMETRON_VERSION:-}"
PREFIX_DEFAULT="$HOME/.local"
PREFIX="${TELEMETRON_PREFIX:-$PREFIX_DEFAULT}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    printf 'telemetron-install: required command not found: %s\n' "$1" >&2
    exit 1
  }
}

need curl
need tar
need uname
need sed

# -------- detect OS --------
os_raw="$(uname -s)"
case "$os_raw" in
  Linux)  os=linux ;;
  Darwin) os=darwin ;;
  *)
    printf 'telemetron-install: unsupported OS: %s\n' "$os_raw" >&2
    printf '  supported: linux, darwin\n' >&2
    exit 1
    ;;
esac

# -------- detect arch --------
arch_raw="$(uname -m)"
case "$arch_raw" in
  x86_64|amd64)          arch=amd64 ;;
  aarch64|arm64)         arch=arm64 ;;
  *)
    printf 'telemetron-install: unsupported architecture: %s\n' "$arch_raw" >&2
    printf '  supported: amd64, arm64\n' >&2
    exit 1
    ;;
esac

# -------- resolve version --------
if [ -z "$VERSION" ]; then
  VERSION="$(
    curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
      | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' \
      | head -n 1
  )"
  if [ -z "$VERSION" ]; then
    printf 'telemetron-install: could not resolve latest release tag for %s\n' "$REPO" >&2
    printf '  check https://github.com/%s/releases and set TELEMETRON_VERSION\n' "$REPO" >&2
    exit 1
  fi
fi

# goreleaser strips the leading "v" in archive filenames, e.g.
#   tag=v0.3.0 -> telemetron_0.3.0_linux_arm64.tar.gz
version_plain="${VERSION#v}"
archive="telemetron_${version_plain}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$VERSION"

printf 'telemetron-install: repo=%s version=%s os=%s arch=%s\n' "$REPO" "$VERSION" "$os" "$arch"

# -------- download into a tempdir --------
tmp="$(mktemp -d 2>/dev/null || mktemp -d -t telemetron-install)"
trap 'rm -rf "$tmp"' EXIT INT TERM

printf 'telemetron-install: downloading %s/%s\n' "$base" "$archive"
curl -fsSL -o "$tmp/$archive"       "$base/$archive"
curl -fsSL -o "$tmp/checksums.txt"  "$base/checksums.txt"

# -------- verify sha256 --------
(
  cd "$tmp"
  expected_line="$(grep "  $archive$" checksums.txt || true)"
  if [ -z "$expected_line" ]; then
    printf 'telemetron-install: %s not listed in checksums.txt\n' "$archive" >&2
    exit 1
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    printf '%s\n' "$expected_line" | sha256sum -c -
  elif command -v shasum >/dev/null 2>&1; then
    printf '%s\n' "$expected_line" | shasum -a 256 -c -
  else
    printf 'telemetron-install: neither sha256sum nor shasum found; refusing to install unverified binary\n' >&2
    exit 1
  fi
)

# -------- extract --------
tar -xzf "$tmp/$archive" -C "$tmp"
if [ ! -f "$tmp/telemetron" ]; then
  printf 'telemetron-install: extracted archive does not contain telemetron binary\n' >&2
  exit 1
fi

# -------- install --------
bindir="$PREFIX/bin"
mkdir -p "$bindir"
# If the target exists and is not writable by this user, prompt sudo fallback
if [ -e "$bindir/telemetron" ] && [ ! -w "$bindir/telemetron" ]; then
  if command -v sudo >/dev/null 2>&1; then
    printf 'telemetron-install: %s exists and is not writable; using sudo\n' "$bindir/telemetron"
    sudo install -m 0755 "$tmp/telemetron" "$bindir/telemetron"
  else
    printf 'telemetron-install: %s is not writable and sudo is unavailable\n' "$bindir/telemetron" >&2
    exit 1
  fi
else
  install -m 0755 "$tmp/telemetron" "$bindir/telemetron"
fi

printf '\ntelemetron-install: installed %s\n' "$bindir/telemetron"
"$bindir/telemetron" version || true

cat <<'EOF'

Next steps:
  1. Put the install prefix bin dir on your PATH if it is not already:
       export PATH="$HOME/.local/bin:$PATH"
  2. Verify the binary:
       telemetron version
       telemetron --help
  3. For a systemd service install (Linux), see:
       telemetron install --help

To opt out of telemetry before starting the service, set one of:
  DO_NOT_TRACK=1
  TELEMETRON_TELEMETRY=0
or create the marker file:
  mkdir -p ~/.telemetron && touch ~/.telemetron/telemetry-off
EOF
