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
#   TELEMETRON_VERSION     pin a specific release tag (default: latest)
#   TELEMETRON_PREFIX      install root (default: $HOME/.local, or /usr/local with sudo)
#
# Auto-setup (runs `telemetron setup` at the end when all three are set):
#   TELEMETRON_ENDPOINT      OTLP/HTTP endpoint URL
#   TELEMETRON_TOKEN         bearer token (takes precedence over token-file)
#   TELEMETRON_TOKEN_FILE    path to a file containing the bearer token
#   TELEMETRON_TOKEN_SECRET  AWS Secrets Manager secret id (fetched via `aws`)
#   TELEMETRON_SETUP_ARGS    extra args appended verbatim to `telemetron setup`
#
# Exactly one of TELEMETRON_TOKEN, TELEMETRON_TOKEN_FILE, or
# TELEMETRON_TOKEN_SECRET is required for auto-setup. When an endpoint
# is set but no token source is given, the installer exits non-zero.
# When neither endpoint nor token are set, setup is skipped and the
# script behaves as before.
#
# The script is POSIX shell and uses only curl, tar, sha256sum|shasum, uname, sed.
# Auto-setup requires root (it writes /etc/telemetron/* and the systemd unit);
# the script will re-exec itself under sudo if needed.

set -eu

REPO="inceptionstack/telemetron"
VERSION="${TELEMETRON_VERSION:-}"
PREFIX_DEFAULT="$HOME/.local"
PREFIX="${TELEMETRON_PREFIX:-$PREFIX_DEFAULT}"

SETUP_ENDPOINT="${TELEMETRON_ENDPOINT:-}"
SETUP_TOKEN="${TELEMETRON_TOKEN:-}"
SETUP_TOKEN_FILE="${TELEMETRON_TOKEN_FILE:-}"
SETUP_TOKEN_SECRET="${TELEMETRON_TOKEN_SECRET:-}"
SETUP_ARGS="${TELEMETRON_SETUP_ARGS:-}"

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

# -------- optional auto-setup --------
# If the caller supplied both an endpoint and a token source, run
# `telemetron setup` non-interactively so the whole thing is one call.
if [ -n "$SETUP_ENDPOINT" ] || [ -n "$SETUP_TOKEN" ] || [ -n "$SETUP_TOKEN_FILE" ] || [ -n "$SETUP_TOKEN_SECRET" ]; then
  if [ -z "$SETUP_ENDPOINT" ]; then
    printf 'telemetron-install: TELEMETRON_ENDPOINT is required when a token source is set\n' >&2
    exit 1
  fi
  if [ -z "$SETUP_TOKEN" ] && [ -z "$SETUP_TOKEN_FILE" ] && [ -z "$SETUP_TOKEN_SECRET" ]; then
    printf 'telemetron-install: TELEMETRON_ENDPOINT is set but no token source was provided\n' >&2
    printf '  set one of TELEMETRON_TOKEN, TELEMETRON_TOKEN_FILE, TELEMETRON_TOKEN_SECRET\n' >&2
    exit 1
  fi

  # Resolve the token to /etc/telemetron/token. Root required.
  token_path="/etc/telemetron/token"

  maybe_sudo=""
  if [ "$(id -u)" -ne 0 ]; then
    if command -v sudo >/dev/null 2>&1; then
      maybe_sudo="sudo"
    else
      printf 'telemetron-install: auto-setup requires root; re-run with sudo\n' >&2
      exit 1
    fi
  fi

  printf '\ntelemetron-install: running auto-setup\n'
  $maybe_sudo install -d -m 0755 /etc/telemetron

  if [ -n "$SETUP_TOKEN" ]; then
    printf '%s\n' "$SETUP_TOKEN" | $maybe_sudo tee "$token_path" >/dev/null
  elif [ -n "$SETUP_TOKEN_FILE" ]; then
    if [ "$SETUP_TOKEN_FILE" != "$token_path" ]; then
      $maybe_sudo install -m 0400 "$SETUP_TOKEN_FILE" "$token_path"
    fi
  else
    # TELEMETRON_TOKEN_SECRET: fetch from AWS Secrets Manager
    need aws
    region="${AWS_REGION:-${AWS_DEFAULT_REGION:-us-east-1}}"
    aws secretsmanager get-secret-value \
        --region "$region" \
        --secret-id "$SETUP_TOKEN_SECRET" \
        --query SecretString --output text \
      | $maybe_sudo tee "$token_path" >/dev/null
  fi
  $maybe_sudo chmod 0400 "$token_path"

  # shellcheck disable=SC2086
  $maybe_sudo env PATH="$PATH" "$bindir/telemetron" setup \
    --non-interactive --yes \
    --endpoint "$SETUP_ENDPOINT" \
    --token-file "$token_path" \
    $SETUP_ARGS

  printf '\ntelemetron-install: auto-setup complete\n'
  exit 0
fi

cat <<'EOF'

Next steps:
  1. Put the install prefix bin dir on your PATH if it is not already:
       export PATH="$HOME/.local/bin:$PATH"
  2. Verify the binary:
       telemetron version
       telemetron --help
  3. Wire it to your OTLP gateway (recommended):
       sudo telemetron setup --endpoint <url> --token-file <path>

Shortcut: set TELEMETRON_ENDPOINT and TELEMETRON_TOKEN (or
TELEMETRON_TOKEN_FILE / TELEMETRON_TOKEN_SECRET) before running this
installer and it will do the setup step for you.
EOF
