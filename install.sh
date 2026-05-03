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

set -e

if [ "${1:-}" = "--help" ] || [ "${1:-}" = "-h" ]; then
  cat <<'EOF'
telemetron installer

Usage:
  sh install.sh

Optional environment:
  TELEMETRON_VERSION       pin a specific release tag (default: latest)
  TELEMETRON_PREFIX        install root (default: $HOME/.local)
  TELEMETRON_ENDPOINT      OTLP/HTTP endpoint URL for auto-setup
  TELEMETRON_TOKEN         bearer token for auto-setup
  TELEMETRON_TOKEN_FILE    path to a file containing the bearer token
  TELEMETRON_TOKEN_SECRET  AWS Secrets Manager secret id fetched via `aws`
  TELEMETRON_SETUP_ARGS    extra args appended verbatim to `telemetron setup`

Exactly one of TELEMETRON_TOKEN, TELEMETRON_TOKEN_FILE, or
TELEMETRON_TOKEN_SECRET is required for auto-setup.
EOF
  exit 0
fi

REPO="inceptionstack/telemetron"
VERSION="${TELEMETRON_VERSION:-}"
HOME_DEFAULT="${HOME:-}"
if [ -z "$HOME_DEFAULT" ]; then
  HOME_DEFAULT="$(cd ~ 2>/dev/null && pwd || printf '/tmp')"
fi
PREFIX_DEFAULT="$HOME_DEFAULT/.local"
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
# If the caller supplied an endpoint and a token source, run
# `telemetron setup` non-interactively so the whole thing is one call.
if [ -n "$SETUP_ENDPOINT" ] || [ -n "$SETUP_TOKEN" ] || [ -n "$SETUP_TOKEN_FILE" ] || [ -n "$SETUP_TOKEN_SECRET" ]; then
  if [ "$os" = "darwin" ]; then
    printf 'telemetron-install: binary installed; skipping auto-setup because systemd service auto-setup is Linux-only\n'
    printf '  run telemetron manually or under launchd on macOS; see docs/macos.md\n'
    exit 0
  fi

  if [ -z "$SETUP_ENDPOINT" ]; then
    printf 'telemetron-install: TELEMETRON_ENDPOINT is required when a token source is set\n' >&2
    exit 1
  fi

  # Enforce exactly one token source.
  token_sources=0
  [ -n "$SETUP_TOKEN" ]        && token_sources=$((token_sources + 1))
  [ -n "$SETUP_TOKEN_FILE" ]   && token_sources=$((token_sources + 1))
  [ -n "$SETUP_TOKEN_SECRET" ] && token_sources=$((token_sources + 1))
  if [ "$token_sources" -eq 0 ]; then
    printf 'telemetron-install: TELEMETRON_ENDPOINT is set but no token source was provided\n' >&2
    printf '  set one of TELEMETRON_TOKEN, TELEMETRON_TOKEN_FILE, TELEMETRON_TOKEN_SECRET\n' >&2
    exit 1
  fi
  if [ "$token_sources" -gt 1 ]; then
    printf 'telemetron-install: multiple token sources set; choose exactly one of\n' >&2
    printf '  TELEMETRON_TOKEN, TELEMETRON_TOKEN_FILE, TELEMETRON_TOKEN_SECRET\n' >&2
    exit 1
  fi

  # Resolve the token to /etc/telemetron/token. Root required; fail
  # fast *before* we touch anything so we don't leave the host in a
  # half-configured state.
  token_path="/etc/telemetron/token"

  maybe_sudo=""
  if [ "$(id -u)" -ne 0 ]; then
    if command -v sudo >/dev/null 2>&1; then
      maybe_sudo="sudo"
      # Prime the sudo credential cache up front so a password prompt
      # cannot land between the token write and the setup call.
      if ! sudo -v; then
        printf 'telemetron-install: auto-setup requires root but sudo refused\n' >&2
        exit 1
      fi
    else
      printf 'telemetron-install: auto-setup requires root; re-run with sudo\n' >&2
      exit 1
    fi
  fi

  printf '\ntelemetron-install: running auto-setup\n'

  # -------- resolve the token value into a variable --------
  # We capture the token in-process first, validate it, then write to
  # disk atomically with mode 0400 in a single step. This avoids the
  # umask 022 race on `tee` and the pipefail gap under POSIX sh when
  # `aws` fails.
  token_value=""
  if [ -n "$SETUP_TOKEN" ]; then
    token_value="$SETUP_TOKEN"
  elif [ -n "$SETUP_TOKEN_FILE" ]; then
    if [ ! -f "$SETUP_TOKEN_FILE" ]; then
      printf 'telemetron-install: TELEMETRON_TOKEN_FILE=%s not found\n' "$SETUP_TOKEN_FILE" >&2
      exit 1
    fi
    # Readable by this user? (If not, a privileged source file is the
    # caller's responsibility; we cannot safely cat it without sudo.)
    if [ ! -r "$SETUP_TOKEN_FILE" ]; then
      if [ -n "$maybe_sudo" ]; then
        token_value="$($maybe_sudo cat "$SETUP_TOKEN_FILE")"
      else
        printf 'telemetron-install: cannot read %s\n' "$SETUP_TOKEN_FILE" >&2
        exit 1
      fi
    else
      token_value="$(cat "$SETUP_TOKEN_FILE")"
    fi
  else
    # TELEMETRON_TOKEN_SECRET: fetch from AWS Secrets Manager.
    need aws
    region="${AWS_REGION:-${AWS_DEFAULT_REGION:-us-east-1}}"
    if ! token_value="$(aws secretsmanager get-secret-value \
          --region "$region" \
          --secret-id "$SETUP_TOKEN_SECRET" \
          --query SecretString --output text 2>/dev/null)"; then
      printf 'telemetron-install: aws secretsmanager get-secret-value failed for %s (region=%s)\n' \
        "$SETUP_TOKEN_SECRET" "$region" >&2
      exit 1
    fi
    # `--output text` returns the literal string "None" when SecretString
    # is absent (binary secrets) or the secret is empty.
    if [ "$token_value" = "None" ]; then
      printf 'telemetron-install: secret %s has no SecretString (binary-only?)\n' "$SETUP_TOKEN_SECRET" >&2
      exit 1
    fi
  fi

  # Strip a single trailing newline if the source had one; everything
  # else (whitespace, CR) stays intact so we don't silently mangle the
  # token.
  token_value="${token_value%"
"}"
  if [ -z "$token_value" ]; then
    printf 'telemetron-install: resolved token is empty; refusing to write\n' >&2
    exit 1
  fi

  # -------- write token atomically, never world-readable --------
  $maybe_sudo install -d -m 0755 /etc/telemetron
  # Stage under the final directory so the rename is same-filesystem
  # and atomic. umask 077 ensures the staged file is never more
  # permissive than 0600 before we tighten it to 0400.
  token_staged="$($maybe_sudo sh -c 'umask 077 && mktemp /etc/telemetron/token.XXXXXX')"
  trap '[ -n "$token_staged" ] && $maybe_sudo rm -f "$token_staged" 2>/dev/null; rm -rf "$tmp"' EXIT INT TERM
  # Write the token via stdin so it never appears in argv. No trailing
  # newline: bearer tokens are presented verbatim as HTTP header values,
  # and a stray LF at the end can corrupt the Authorization header or
  # break strict authorizer regexes (observed in the loki-telemetry
  # authorizer — see commit log).
  printf '%s' "$token_value" | $maybe_sudo tee "$token_staged" >/dev/null
  $maybe_sudo chmod 0400 "$token_staged"
  $maybe_sudo chown root:root "$token_staged" 2>/dev/null || true
  $maybe_sudo mv -f "$token_staged" "$token_path"
  token_staged=""
  # Clear the in-process copy. POSIX sh has no zeroization; unset is
  # the best we can do short of overwriting memory.
  unset token_value SETUP_TOKEN

  # -------- run setup --------
  # SETUP_ARGS is intentionally passed unquoted so multi-word values
  # split into argv. This is a trusted local env var; document that
  # callers must not pass untrusted input here.
  # shellcheck disable=SC2086
  $maybe_sudo env PATH="$PATH" TELEMETRON_TOKEN_SECRET="$SETUP_TOKEN_SECRET" "$bindir/telemetron" setup \
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
