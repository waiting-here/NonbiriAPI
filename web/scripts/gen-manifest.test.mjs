import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
import { copyFile, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { promisify } from 'node:util';
import test from 'node:test';

const execute = promisify(execFile);

test('asset manifests are reproducible and exclude their previous output', async (t) => {
  const root = await mkdtemp(join(tmpdir(), 'nonbiri-manifest-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  await mkdir(join(root, 'scripts'));
  const script = join(root, 'scripts', 'gen-manifest.mjs');
  await copyFile(new URL('./gen-manifest.mjs', import.meta.url), script);
  for (const app of ['admin', 'user']) {
    await mkdir(join(root, 'dist', app, 'assets'), { recursive: true });
    await writeFile(join(root, 'dist', app, 'index.html'), `<main>${app}</main>`);
    await writeFile(join(root, 'dist', app, 'assets', 'app.js'), 'console.log("ready");\n');
  }
  const manifests = () =>
    Promise.all(
      ['admin', 'user'].map((app) =>
        readFile(join(root, 'dist', app, 'build-manifest.json'), 'utf8'),
      ),
    );
  await execute(process.execPath, [script]);
  const first = await manifests();
  await execute(process.execPath, [script]);
  assert.deepEqual(await manifests(), first);
  for (const raw of first) {
    const manifest = JSON.parse(raw);
    assert.deepEqual(Object.keys(manifest.files), ['assets/app.js', 'index.html']);
    assert.equal(
      manifest.files['assets/app.js'],
      `sha256:${createHash('sha256').update('console.log("ready");\n').digest('hex')}`,
    );
  }

  await writeFile(join(root, 'dist', 'user', 'assets', 'app.js'), 'console.log("changed");\n');
  await execute(process.execPath, [script]);
  const changed = await manifests();
  assert.equal(changed[0], first[0]);
  assert.notEqual(changed[1], first[1]);
  assert.equal(
    JSON.parse(changed[1]).files['index.html'],
    JSON.parse(first[1]).files['index.html'],
  );
});
