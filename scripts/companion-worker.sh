#!/bin/sh
# Install this launcher and nonbiri-companion-worker together. The service
# account needs an active user systemd manager with cgroup delegation.
set -eu
worker_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
case "${1:-}" in
  run) memory=384M; duration=23s ;;
  compile) memory=1G; duration=95s ;;
  *) exit 3 ;;
esac
exec systemd-run --user --quiet --pipe --wait --collect \
  -p "MemoryMax=$memory" -p MemorySwapMax=0 -p TasksMax=64 \
  -p CPUQuota=100% -p "RuntimeMaxSec=$duration" -p KillMode=control-group \
  "$worker_dir/nonbiri-companion-worker" "$1"
