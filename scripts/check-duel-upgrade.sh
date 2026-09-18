#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
go_command=${GO:-go}
python_command=${PYTHON:-python3}
released_commit=7ed0822c8ae7191421645c638a322a2411b1c8a9
test "$(git rev-parse 'v1.0.0-beta.4^{commit}')" = "$released_commit"
temporary_base=$(cd "${TMPDIR:-/tmp}" && pwd -P)
temporary=$(mktemp -d "$temporary_base/nonbiri-upgrade.XXXXXXXX")
cleanup() {
    local resolved
    resolved=$(cd "$temporary" && pwd -P) || return
    case "$resolved" in
        "$temporary_base"/nonbiri-upgrade.*) rm -rf -- "$resolved" ;;
        *) return 1 ;;
    esac
}
trap cleanup EXIT
mkdir "$temporary/released" "$temporary/data"
git archive "$released_commit" | tar -x -C "$temporary/released"
"$python_command" - "$root" "$temporary" <<'PY'
import json, pathlib, sys
root, temporary = map(pathlib.Path, sys.argv[1:])
legacy = temporary / "released"
source = (root / "internal/db/testdata/released_gameplay_fixture_test.go.txt").read_text(encoding="utf-8")
source = source.replace("gameWireFixture", "releasedGameWireFixture").replace("newGameWireFixture", "newReleasedGameWireFixture")
source = source.replace("f94972e6544ee5020c6a16451d4213c0cdef61b51814b6216ba9291e3db9734c", "346a89c664c68eb4c566118c4f0b4f4ba6f3ae2f5584ede333fb6f88070e83bb")
assert source.count("charge != 5") == 1
source = source.replace("charge != 5", "charge != 7").replace("old cap was not reproduced", "released actual charge was not preserved")
for indent, user in [("\t", "userID"), ("\t\t", "id")]:
    needle = indent + 'external, err := ledger.CodedAccount(ctx, tx, "external")'
    assert source.count("\n" + needle + "\n") == 1
    source = source.replace("\n" + needle + "\n", "\n" + indent + f'if _, err := ledger.CreateUserAssetAccount(ctx, tx, {user}, ledger.Game, time.Now().Unix()); err != nil {{ t.Fatal(err) }}\n' + needle + "\n")
gameplay = temporary / "gameplay.go"
gameplay.write_text(source, encoding="utf-8", newline="\n")
replacements = {
    str(legacy / "released_gameplay_fixture_test.go"): str(gameplay),
    str(legacy / "internal/ledger/released_dual_wallet_fixture_test.go"): str(root / "internal/db/testdata/released_dual_wallet_fixture_test.go.txt"),
}
(temporary / "overlay.json").write_text(json.dumps({"Replace": replacements}), encoding="utf-8", newline="\n")
PY
export CGO_ENABLED=0
export NONBIRI_DUAL_WALLET_FIXTURE="$temporary/data/wallet.db"
export NONBIRI_DUAL_UPGRADED_FIXTURE="$temporary/data/upgraded.db"
export NONBIRI_GAMEPLAY_FIXTURE="$temporary/data/gameplay.db"
export NONBIRI_BILLING_FIXTURE="$temporary/data/billing.db"
(
    cd "$temporary/released"
    "$go_command" test -c -overlay "$temporary/overlay.json" -o "$temporary/released-app.test" .
    "$go_command" test -c -overlay "$temporary/overlay.json" -o "$temporary/released-wallet.test" ./internal/ledger
    "$temporary/released-app.test" -test.run '^TestWriteReleased(Gameplay|Billing)Fixture$' -test.v
    "$temporary/released-wallet.test" -test.run '^TestWriteReleasedDualWalletFixture$' -test.v
)
"$go_command" test -count=1 -v -run '^TestDuelUpgradeFromReleasedBinary$' ./internal/db
NONBIRI_DUAL_GAMEPLAY_FIXTURE="$NONBIRI_GAMEPLAY_FIXTURE" NONBIRI_DUAL_BILLING_FIXTURE="$NONBIRI_BILLING_FIXTURE" \
    "$go_command" test -count=1 -v -run '^TestReleasedDualAsset(Gameplay|Billing)Upgrade$' .
"$temporary/released-wallet.test" -test.run '^TestReleasedRejectsDuelUpgrade$' -test.v
printf 'Released source: %s\n' "$released_commit"
"$go_command" version
sha256sum "$temporary/data/"*.db
