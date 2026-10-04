#!/usr/bin/env bash
# Retrieve only an exact historical object from the configured project origin.
set -euo pipefail
cd "$(dirname "$0")/.."
test "$#" -eq 1
source_commit=$1
case "$source_commit" in
  8949a3d6e5b3d7536549f42a4c597393fccab62a) ;;
  *) printf 'Unrecognized released source commit\n' >&2; exit 2 ;;
esac
if ! git cat-file -e "$source_commit^{commit}" 2>/dev/null; then
  timeout 120s git -c http.lowSpeedLimit=1 -c http.lowSpeedTime=30 \
    fetch --no-tags --depth=1 origin "$source_commit"
fi
test "$(git rev-parse "$source_commit^{commit}")" = "$source_commit"
