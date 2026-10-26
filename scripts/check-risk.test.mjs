import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';

test('local verification consumes generated scope and propagates a selected test failure', () => {
  const directory = mkdtempSync(join(tmpdir(), 'nonbiri-risk-'));
  try {
    const go = join(directory, 'go-fixture.sh').replaceAll('\\', '/');
    const log = join(directory, 'commands.txt');
    writeFileSync(go, `#!/usr/bin/env bash
set -euo pipefail
if [ "$1" = run ]; then
  while [ "$1" != -github-output ]; do shift; done
  printf 'mode=routine\\ngo=true\\nrace=false\\nweb=false\\nupgrade=false\\ngo_packages=example/changed\\nrace_packages=\\nbrowser=false\\n' > "$2"
else
  printf '%s\\n' "$*" >> "$COMMAND_LOG"
  if [ "$1" = test ]; then exit "$TEST_STATUS"; fi
fi
`, { mode: 0o755 });
    const run = (status) => spawnSync(process.env.BASH_EXECUTABLE || 'bash', ['scripts/check-risk.sh', '--base', 'fixture', '--component', 'go'], {
      cwd: resolve(import.meta.dirname, '..'), encoding: 'utf8', timeout: 30_000,
      env: { ...process.env, GO: go, COMMAND_LOG: log, TEST_STATUS: String(status) },
    });
    assert.equal(run(0).status, 0);
    assert.match(readFileSync(log, 'utf8'), /build example\/changed\nvet example\/changed\ntest -vet=off -timeout=30m example\/changed/);
    const failed = run(7);
    assert.equal(failed.status, 7, failed.stderr);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
