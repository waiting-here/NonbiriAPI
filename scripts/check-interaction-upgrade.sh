#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
go_command=${GO:-go}
python_command=${PYTHON:-python3}
case "${1:-interaction}" in
    interaction)
        released_commit=37e060ab0d0f29d632fe6b8036839b413388812a
        schema_hash=c5656895b47dcf9b9893de580d72ef2dd6c2bea530e8f9f28ef304377d01bfdd
        manifest_hash=66319db6ffcd94d214725a7f21b3b10b8efb9bdc055d17acc14579cde6c86009
        upgrade_test=TestInteractionUpgradeFromReleasedBinary
        ;;
    storage)
        released_commit=8a46c72d911a914eabcb7ef17c537e7ac12d6969
        schema_hash=c692572f9a8c7ba0785808c72a59585d9298d9a1da4b7c90a15878b5a26b9386
        manifest_hash=3f773b6dca01058f2296f437c3666afde92a74e8eeb861fa8637756dcd859481
        upgrade_test=TestStorageContractsUpgradeFromReleasedBinary
        ;;
    *) printf '%s
' 'Unknown upgrade profile' >&2; exit 2 ;;
esac
bash "$root/scripts/ensure-upgrade-source.sh" "$released_commit"
temporary_base=$(cd "${TMPDIR:-/tmp}" && pwd -P)
temporary=$(mktemp -d "$temporary_base/nonbiri-interaction-upgrade.XXXXXXXX")
cleanup() {
    if [[ "${NONBIRI_UPGRADE_KEEP_TEMP:-0}" == 1 ]]; then
        printf 'Upgrade fixture workspace: %s\n' "$temporary"
        return
    fi
    local resolved
    resolved=$(cd "$temporary" && pwd -P) || return
    case "$resolved" in
        "$temporary_base"/nonbiri-interaction-upgrade.*) rm -rf -- "$resolved" ;;
        *) return 1 ;;
    esac
}
trap cleanup EXIT
mkdir "$temporary/released" "$temporary/data"
git archive "$released_commit" | tar -x -C "$temporary/released"
"$python_command" - "$root" "$temporary" "$schema_hash" "$manifest_hash" <<'PY'
import json, pathlib, sys
root, temporary = map(pathlib.Path, sys.argv[1:3])
schema_hash, manifest_hash = sys.argv[3:]
legacy = temporary / "released"
fixture = (root / "internal/db/testdata/released_dual_wallet_fixture_test.go.txt").read_text()
for old, new in {
    "dcce93b162d6f9540ec45118fbcc9661b633119fe35037213506a8f956117098": schema_hash,
    "cbab638c0f8c97efd0037f47cdcff58575de714dd47390c9e9e9039a9886f517": manifest_hash,
    "230a5c1ec309bd392d56905bfec5a5aaf790a8a96718de3f2f0f667d6271b8f3": "d07ead0c73eb8173f5e88cc463601bac7b72b576f22361db87e0da7a1cc0c1fc",
}.items():
    assert fixture.count(old) == 1
    fixture = fixture.replace(old, new)
generated = temporary / "released-wallet-fixture.go"
generated.write_text(fixture, encoding="utf-8", newline="\n")
replacements = {
    str(legacy / "internal/ledger/released_governance_facts_test.go"): str(root / "internal/db/testdata/released_governance_facts_test.go.txt"),
    str(legacy / "internal/ledger/released_dual_wallet_fixture_test.go"): str(generated),
}
(temporary / "overlay.json").write_text(json.dumps({"Replace": replacements}), encoding="utf-8", newline="\n")
PY
export CGO_ENABLED=0
export NONBIRI_DUAL_WALLET_FIXTURE="$temporary/data/wallet.db"
unset NONBIRI_INTERACTION_FIXTURE NONBIRI_STORAGE_FIXTURE
if [[ "${1:-interaction}" == storage ]]; then
    export NONBIRI_STORAGE_FIXTURE="$NONBIRI_DUAL_WALLET_FIXTURE"
else
    export NONBIRI_INTERACTION_FIXTURE="$NONBIRI_DUAL_WALLET_FIXTURE"
fi
unset NONBIRI_INTERACTION_MASTER_KEY_FILE
(
    # Released binaries create fixtures from their own schema.
    unset NONBIRI_RACE_TEMPLATE_PATH NONBIRI_RACE_TEMPLATE_SHA256 NONBIRI_RACE_TEMPLATE_ID
    cd "$temporary/released"
    "$go_command" test -c -overlay "$temporary/overlay.json" -o "$temporary/released-wallet.test" ./internal/ledger
    "$temporary/released-wallet.test" -test.run '^TestWriteReleasedDualWalletFixture$' -test.v -test.timeout 2m
)
"$go_command" test -count=1 -v -timeout=10m -run "^${upgrade_test}$" ./internal/db
printf 'Released source: %s\n' "$released_commit"
"$go_command" version
sha256sum "$temporary/data/wallet.db"
