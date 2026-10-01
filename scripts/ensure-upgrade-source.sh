#!/usr/bin/env bash
# Retrieve only an exact historical object from the configured project origin.
set -euo pipefail
cd "$(dirname "$0")/.."
test "$#" -eq 1
source_commit=$1
case "$source_commit" in
  db959c64674afc531046a63066de0464725d439c|37e060ab0d0f29d632fe6b8036839b413388812a|8a46c72d911a914eabcb7ef17c537e7ac12d6969) ;;
  *) printf 'Unrecognized released source commit\n' >&2; exit 2 ;;
esac
if ! git cat-file -e "$source_commit^{commit}" 2>/dev/null; then
  timeout 120s git -c http.lowSpeedLimit=1 -c http.lowSpeedTime=30 \
    fetch --no-tags --depth=1 origin "$source_commit"
fi
test "$(git rev-parse "$source_commit^{commit}")" = "$source_commit"
