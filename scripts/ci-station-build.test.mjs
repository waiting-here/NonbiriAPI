import test from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, relative, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
import { packStationBuild, restoreStationBuild } from './ci-station-build.mjs';
import auditSetup from '../web/test/audit/global-setup.mjs';
import picturebookSetup from '../web/test/picturebook/global-setup.mjs';
import fatfishSetup from '../web/test/fatfish/global-setup.mjs';

const hash = (bytes) => `sha256:${createHash('sha256').update(bytes).digest('hex')}`;

function put(root, path, bytes) {
  const full = join(root, path);
  mkdirSync(dirname(full), { recursive: true });
  writeFileSync(full, bytes);
}

function fixture(t) {
  const root = mkdtempSync(join(tmpdir(), 'station-build-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  put(root, 'web/public/assets/shared.webp', Buffer.from([0, 255, 7, 128]));
  put(root, 'web/public/assets/conflict.webp', 'original public image');
  for (const app of ['admin', 'user']) {
    const files = {
      'index.html': Buffer.from(`<html>${app}</html>`),
      'assets/shared.webp': readFileSync(join(root, 'web/public/assets/shared.webp')),
      'assets/conflict.webp': Buffer.from(app === 'admin' ? 'transformed station image' : 'original public image'),
      'assets/game-hashed.flac': Buffer.from([1, 0, 255, 128, 2]),
    };
    for (const [path, bytes] of Object.entries(files)) put(root, `web/dist/${app}/${path}`, bytes);
    put(root, `web/dist/${app}/build-manifest.json`, JSON.stringify({ app, files: Object.fromEntries(Object.entries(files).map(([path, bytes]) => [path, hash(bytes)])) }, null, 2) + String.fromCharCode(10));
  }
  return root;
}

function snapshot(directory) {
  const files = {};
  function visit(path) {
    for (const entry of readdirSync(path, { withFileTypes: true })) {
      const full = join(path, entry.name);
      if (entry.isDirectory()) visit(full);
      else files[relative(directory, full)] = readFileSync(full);
    }
  }
  visit(directory);
  return files;
}

test('both stations roundtrip with generated media and differing public-name output intact', (t) => {
  const root = fixture(t);
  const original = snapshot(join(root, 'web/dist'));
  assert.deepEqual(packStationBuild(root, '.cache/station'), { omitted: 3, retained: 5 });
  assert.equal(existsSync(join(root, '.cache/station/admin/assets/shared.webp')), false);
  assert.equal(existsSync(join(root, '.cache/station/user/assets/conflict.webp')), false);
  assert.equal(readFileSync(join(root, '.cache/station/admin/assets/conflict.webp'), 'utf8'), 'transformed station image');
  assert.deepEqual(readFileSync(join(root, '.cache/station/user/assets/game-hashed.flac')), Buffer.from([1, 0, 255, 128, 2]));
  rmSync(join(root, 'web/dist'), { recursive: true });
  assert.deepEqual(restoreStationBuild(root, '.cache/station'), { restored: 8 });
  assert.deepEqual(snapshot(join(root, 'web/dist')), original);
});

test('a changed public source is retained rather than substituted for built output', (t) => {
  const root = fixture(t);
  put(root, 'web/public/assets/shared.webp', 'changed after build');
  assert.deepEqual(packStationBuild(root, '.cache/station'), { omitted: 1, retained: 7 });
  rmSync(join(root, 'web/dist'), { recursive: true });
  restoreStationBuild(root, '.cache/station');
  assert.deepEqual(readFileSync(join(root, 'web/dist/user/assets/shared.webp')), Buffer.from([0, 255, 7, 128]));
});

test('packing rejects changed, missing and unlisted build inputs', (t) => {
  for (const change of ['changed', 'missing', 'unlisted']) {
    const root = fixture(t);
    if (change === 'changed') put(root, 'web/dist/admin/index.html', 'changed build');
    if (change === 'missing') rmSync(join(root, 'web/dist/admin/index.html'));
    if (change === 'unlisted') put(root, 'web/dist/admin/unlisted.js', 'extra');
    assert.throws(() => packStationBuild(root, '.cache/station'), /hash mismatch|file list differs/);
  }
});

test('restoring rejects changed or missing omitted public inputs', (t) => {
  for (const change of ['changed', 'missing']) {
    const root = fixture(t);
    packStationBuild(root, '.cache/station');
    rmSync(join(root, 'web/dist'), { recursive: true });
    if (change === 'changed') put(root, 'web/public/assets/shared.webp', 'changed checkout input');
    else rmSync(join(root, 'web/public/assets/shared.webp'));
    assert.throws(() => restoreStationBuild(root, '.cache/station'), /hash mismatch|ENOENT/);
  }
});

test('restoring rejects corrupted, missing and unlisted generated artifact files', (t) => {
  for (const change of ['changed', 'missing', 'unlisted', 'unlisted-root']) {
    const root = fixture(t);
    packStationBuild(root, '.cache/station');
    rmSync(join(root, 'web/dist'), { recursive: true });
    if (change === 'changed') put(root, '.cache/station/admin/index.html', 'corrupt artifact');
    if (change === 'missing') rmSync(join(root, '.cache/station/admin/assets/game-hashed.flac'));
    if (change === 'unlisted') put(root, '.cache/station/admin/unlisted.js', 'extra');
    if (change === 'unlisted-root') put(root, '.cache/station/unlisted.js', 'extra');
    assert.throws(() => restoreStationBuild(root, '.cache/station'), /hash mismatch|ENOENT|unlisted file/);
  }
});

test('shared fixture paths bypass local dist checks and still reject missing files and directories', async (t) => {
  const root = fixture(t);
  rmSync(join(root, 'web/dist'), { recursive: true });
  const previousDirectory = process.cwd();
  const variables = ['NONBIRI_BROWSER_FIXTURE_BINARY', 'NONBIRI_AUDIT_BROWSER_STATE', 'NONBIRI_IMAGE_BROWSER_STATE', 'NONBIRI_FISH_BROWSER_STATE'];
  const previous = Object.fromEntries(variables.map((name) => [name, process.env[name]]));
  try {
    process.chdir(join(root, 'web'));
    for (const [setup, variable] of [[auditSetup, variables[1]], [picturebookSetup, variables[2]], [fatfishSetup, variables[3]]]) {
      process.env[variable] = join(root, 'state', variable + '.json');
      delete process.env.NONBIRI_BROWSER_FIXTURE_BINARY;
      await assert.rejects(setup(), /ENOENT/);
      process.env.NONBIRI_BROWSER_FIXTURE_BINARY = join(root, 'missing-fixture');
      await assert.rejects(setup(), /ENOENT/);
      process.env.NONBIRI_BROWSER_FIXTURE_BINARY = root;
      await assert.rejects(setup(), /must be a regular file/);
      // Node exits immediately on the Go test flags, proving the shared path
      // reached process startup without a local build or a Go compiler.
      process.env.NONBIRI_BROWSER_FIXTURE_BINARY = process.execPath;
      await assert.rejects(setup(), /The Go fixture stopped/);
    }
  } finally {
    process.chdir(previousDirectory);
    for (const [name, value] of Object.entries(previous)) {
      if (value === undefined) delete process.env[name];
      else process.env[name] = value;
    }
  }
});

test('outputs are bounded, separate from inputs, and never replace populated directories', (t) => {
  const root = fixture(t);
  for (const path of ['..', '.', 'web', 'web/public/output', 'web/dist/output', '.git/output']) {
    assert.throws(() => packStationBuild(root, path), /inside the repository|overlaps/);
  }
  put(root, '.cache/occupied/keep.txt', 'keep');
  assert.throws(() => packStationBuild(root, '.cache/occupied'), /must be empty/);
  packStationBuild(root, '.cache/station');
  assert.throws(() => restoreStationBuild(root, '.cache/station'), /must be empty/);
  assert.equal(readFileSync(join(root, '.cache/occupied/keep.txt'), 'utf8'), 'keep');
  assert.equal(readFileSync(join(root, 'web/dist/admin/index.html'), 'utf8'), '<html>admin</html>');
});

test('manifest station identity and file paths must match the selected output', (t) => {
  for (const change of ['identity', 'path']) {
    const root = fixture(t);
    const path = join(root, 'web/dist/admin/build-manifest.json');
    const manifest = JSON.parse(readFileSync(path, 'utf8'));
    if (change === 'identity') manifest.app = 'user';
    else manifest.files['../outside'] = hash('outside');
    writeFileSync(path, JSON.stringify(manifest));
    assert.throws(() => packStationBuild(root, '.cache/station'), /Invalid admin station manifest|Invalid station file path/);
  }
});

test('CLI runs from the repository root and returns a real failure for invalid usage', (t) => {
  const root = fixture(t);
  const script = resolve(import.meta.dirname, 'ci-station-build.mjs');
  const run = (...args) => spawnSync(process.execPath, [script, ...args], { cwd: root, encoding: 'utf8' });
  const packed = run('pack', '.cache/station');
  assert.equal(packed.status, 0, packed.stderr);
  assert.deepEqual(JSON.parse(packed.stdout), { omitted: 3, retained: 5 });
  rmSync(join(root, 'web/dist'), { recursive: true });
  const restored = run('restore', '.cache/station');
  assert.equal(restored.status, 0, restored.stderr);
  assert.deepEqual(JSON.parse(restored.stdout), { restored: 8 });
  assert.notEqual(run('invalid', '.cache/station').status, 0);
  assert.notEqual(run('restore').status, 0);
});
