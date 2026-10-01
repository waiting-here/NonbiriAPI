#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
bash "$root/scripts/check-duel-upgrade.sh"
bash "$root/scripts/check-interaction-upgrade.sh"
exec bash "$root/scripts/check-interaction-upgrade.sh" storage
