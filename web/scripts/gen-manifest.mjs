// Records a sha256 hash for every built file of each station, writing
// web/dist/<station>/build-manifest.json. Used for AGPL source-offer
// bookkeeping: the hashes pin the exact artifact set a release was built from.
import { createHash } from 'node:crypto';
import { readFile, readdir, writeFile } from 'node:fs/promises';
import { dirname, join, relative, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

const webDir = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const distDir = join(webDir, 'dist');

async function walk(dir) {
  const files = [];
  const entries = await readdir(dir, { withFileTypes: true });
  for (const entry of entries) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) {
      files.push(...(await walk(full)));
    } else {
      files.push(full);
    }
  }
  return files;
}

function sha256(data) {
  return `sha256:${createHash('sha256').update(data).digest('hex')}`;
}

for (const app of ['admin', 'user']) {
  const appDir = join(distDir, app);
  const files = (await walk(appDir)).sort();
  const manifest = {
    app,
    generatedAt: new Date().toISOString(),
    files: {},
  };
  for (const file of files) {
    const rel = relative(appDir, file).split(sep).join('/');
    manifest.files[rel] = sha256(await readFile(file));
  }
  await writeFile(join(appDir, 'build-manifest.json'), `${JSON.stringify(manifest, null, 2)}\n`);
  console.log(`manifest written for ${app} (${files.length} files)`);
}
