#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Install and configure git-secrets for this repository.
# Run once after cloning:  bash scripts/git-secrets-install.sh
set -euo pipefail

if ! command -v git-secrets >/dev/null 2>&1; then
  echo "git-secrets not found on PATH." >&2
  echo "Install from https://github.com/awslabs/git-secrets" >&2
  echo "  macOS:   brew install git-secrets" >&2
  echo "  Linux:   git clone https://github.com/awslabs/git-secrets && cd git-secrets && sudo make install" >&2
  exit 1
fi

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

# Install the pre-commit, commit-msg, and prepare-commit-msg hooks.
git secrets --install --force

# Register the AWS pattern set.
git secrets --register-aws

# telemetron-specific bearer token patterns.
git secrets --add 'lpk_live_[0-9a-f]{32}'
git secrets --add 'lpk_[a-z]+_[0-9a-f]+'

# Common PAT/token formats.
git secrets --add 'ghp_[A-Za-z0-9]{36,}'
git secrets --add 'github_pat_[A-Za-z0-9_]{82,}'
git secrets --add 'x-access-token:[A-Za-z0-9_-]+'

# Allowed patterns that look like tokens but are documentation placeholders.
git secrets --add --allowed 'replace-with-your-bearer-token'
git secrets --add --allowed 'your-otlp-gateway.example.com'

echo "git-secrets installed and configured. Running an initial scan..."
git secrets --scan
echo "OK. Hooks will now run on every commit."
