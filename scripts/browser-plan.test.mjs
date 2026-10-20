import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
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
  const suites = ['picturebook', 'audit', 'management', 'workflow', 'fatfish', 'lakenotes'];
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
