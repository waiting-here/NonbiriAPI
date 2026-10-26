import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmodSync, mkdtempSync, readFileSync, rmSync, rmdirSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { resolve } from 'node:path';
import { selectSpecs, selectSuites } from './browser-plan.mjs';

test('timing hints balance whole files without dropping new or renamed specs', () => {
  const specs = ['slow.spec.ts', 'medium.spec.ts', 'renamed.spec.ts', 'new.spec.ts'];
  const hints = { default_spec_seconds: 2, spec_seconds: { 'slow.spec.ts': 10, 'medium.spec.ts': 6, 'deleted.spec.ts': 999 } };
  const first = selectSpecs(specs, '1/2', hints);
  const second = selectSpecs(specs, '2/2', hints);
  assert.deepEqual([...first, ...second].sort(), specs.sort());
  assert.equal(first.filter((spec) => second.includes(spec)).length, 0);
  assert.deepEqual(first, ['slow.spec.ts']);
  assert.deepEqual(selectSpecs(specs, 'all'), specs.sort());
});

test('one or no spec is assigned only to a necessary shard', () => {
  assert.deepEqual(selectSpecs(['only.spec.ts'], '1/2'), ['only.spec.ts']);
  assert.deepEqual(selectSpecs(['only.spec.ts'], '2/2'), []);
  assert.deepEqual(selectSpecs([], '1/2'), []);
  for (const shard of ['0/2', '3/2', '1/0', '1/100', 'invalid']) assert.throws(() => selectSpecs([], shard));
});

test('integration groups cover each selected suite exactly once', () => {
  const suites = ['picturebook', 'audit', 'management', 'workflow', 'proxy', 'fatfish', 'lakenotes'];
  const groups = [...selectSuites(suites, 'content'), ...selectSuites(suites, 'management')];
  assert.deepEqual(groups.sort(), suites.sort());
  assert.deepEqual(selectSuites(['management'], 'content'), []);
  assert.throws(() => selectSuites(['new-suite'], 'all'));
});

test('CLI selects from the supplied plan across working directories and rejects invalid input', () => {
  const run = (plan, ...args) => spawnSync(process.execPath, [resolve(import.meta.dirname, 'browser-plan.mjs'), ...args], {
    cwd: resolve(import.meta.dirname, '../web'), encoding: 'utf8',
    env: { ...process.env, BROWSER_PLAN: JSON.stringify(plan) },
  });
  const selected = run({ specs: ['test/e2e/new.spec.ts'], suites: ['management'], engine: false }, 'specs', '1/2');
  assert.equal(selected.status, 0, selected.stderr);
  assert.equal(selected.stdout, 'test/e2e/new.spec.ts\0');
  assert.equal(run({ suites: ['management'] }, 'suites', 'management').stdout, 'management\0');
  assert.notEqual(run({ specs: [] }, 'specs', 'invalid').status, 0);
  assert.notEqual(run(null, 'specs').status, 0);
});

test('CI collects every selected browser suite while preserving failures and local fail-fast', () => {
  const directory = mkdtempSync(resolve(tmpdir(), 'nonbiri-browser-gate-'));
  const npm = resolve(directory, 'npm.sh');
  const trace = resolve(directory, 'commands.txt');
  const portable = (path) => path.replaceAll('\\', '/');
  const bash = process.platform === 'win32'
    ? resolve(process.env.ProgramFiles || 'C:/Program Files', 'Git/bin/bash.exe') : 'bash';
  writeFileSync(npm, '#!/usr/bin/env bash\nset -eu\nprintf "%s\\n" "$*" >> "$RUNNER_TRACE"\ncase " $* " in *" playwright.audit.config.ts "*) exit "$FAIL_CODE" ;; esac\n');
  chmodSync(npm, 0o700);
  try {
    for (const [ci, failure, expectedStatus, expectedCount] of [
      ['true', '17', 1, 4], ['true', '0', 0, 4], ['', '17', 17, 1],
    ]) {
      writeFileSync(trace, '');
      const result = spawnSync(bash, ['--noprofile', '--norc',
        portable(resolve(import.meta.dirname, 'check-web.sh')), 'browser', 'management', 'all'], {
        encoding: 'utf8', timeout: 30_000,
        env: { ...process.env, CI: ci, FAIL_CODE: failure, NPM: portable(npm),
          RUNNER_TRACE: portable(trace), NONBIRI_BROWSER_FIXTURE_BINARY: 'unused-existing-fixture',
          BROWSER_PLAN: JSON.stringify({ suites: ['audit', 'management', 'workflow', 'proxy'] }) },
      });
      assert.equal(result.status, expectedStatus, result.stderr || String(result.error));
      const commands = readFileSync(trace, 'utf8').trim().split('\n');
      assert.equal(commands.length, expectedCount);
      assert.ok(commands.every((line) => line.includes('--max-failures=0') === (ci === 'true')));
      if (ci === 'true') assert.match(commands.at(-1), /playwright\.proxy\.config\.ts/);
    }
  } finally {
    rmSync(trace, { force: true });
    rmSync(npm, { force: true });
    rmdirSync(directory);
  }
});
