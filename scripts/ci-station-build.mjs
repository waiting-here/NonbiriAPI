import { createHash } from 'node:crypto';
import { copyFileSync, existsSync, lstatSync, mkdirSync, readFileSync, readdirSync, realpathSync, writeFileSync } from 'node:fs';
import { dirname, isAbsolute, join, relative, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

const stations = ['admin', 'user'];
const manifestName = 'build-manifest.json';

function within(root, path) {
  const rel = relative(root, path);
  return rel === '' || (!isAbsolute(rel) && rel !== '..' && !rel.startsWith(`..${sep}`));
}

function checkedPath(base, path) {
  if (typeof path !== 'string' || !path || path.includes(String.fromCharCode(92)) || path.includes(':') || path.includes(String.fromCharCode(0)) || path.split('/').some((part) => !part || part === '.' || part === '..')) {
    throw new Error(`Invalid station file path: ${path}`);
  }
  const full = resolve(base, path);
  if (!within(base, full)) throw new Error(`Station file escapes its directory: ${path}`);
  return full;
}

function noLinkedParents(root, path) {
  for (let current = path; current !== root; current = dirname(current)) {
    if (!within(root, current)) throw new Error('Directory escapes the repository.');
    if (existsSync(current) && lstatSync(current).isSymbolicLink()) throw new Error(`Linked path is not supported: ${current}`);
  }
}

function artifactDirectory(root, path) {
  const full = resolve(root, path);
  if (full === root || !within(root, full)) throw new Error('Artifact directory must be inside the repository.');
  for (const protectedPath of ['.git', 'web/dist', 'web/public']) {
    const protectedRoot = resolve(root, protectedPath);
    if (within(protectedRoot, full) || within(full, protectedRoot)) throw new Error('Artifact directory overlaps source or build inputs.');
  }
  noLinkedParents(root, full);
  return full;
}

function requireEmpty(root, path) {
  noLinkedParents(root, path);
  if (existsSync(path) && (!lstatSync(path).isDirectory() || readdirSync(path).length)) {
    throw new Error(`Output directory must be empty: ${path}`);
  }
}

function fileBytes(base, path) {
  const full = checkedPath(base, path);
  if (!lstatSync(full).isFile() || !within(base, realpathSync(full))) throw new Error(`Station input is not a regular file: ${path}`);
  return readFileSync(full);
}

function hash(bytes) {
  return `sha256:${createHash('sha256').update(bytes).digest('hex')}`;
}

function manifestAt(base, station) {
  const bytes = fileBytes(base, manifestName);
  const manifest = JSON.parse(bytes);
  if (manifest.app !== station || !manifest.files || Array.isArray(manifest.files) || typeof manifest.files !== 'object' || !Object.keys(manifest.files).length) {
    throw new Error(`Invalid ${station} station manifest.`);
  }
  for (const [path, expected] of Object.entries(manifest.files)) {
    checkedPath(base, path);
    if (path === manifestName || !/^sha256:[a-f0-9]{64}$/.test(expected)) throw new Error(`Invalid manifest entry: ${path}`);
  }
  return { bytes, files: manifest.files };
}

function inventory(base) {
  const files = [];
  function visit(dir) {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const path = join(dir, entry.name);
      if (entry.isDirectory()) visit(path);
      else if (entry.isFile()) files.push(relative(base, path).split(sep).join('/'));
      else throw new Error(`Station input is not a regular file: ${path}`);
    }
  }
  visit(base);
  return files;
}

function put(base, path, bytes) {
  const full = checkedPath(base, path);
  mkdirSync(dirname(full), { recursive: true });
  writeFileSync(full, bytes);
}

export function packStationBuild(repository, output) {
  const root = realpathSync(repository);
  const target = artifactDirectory(root, output);
  requireEmpty(root, target);
  const publicRoot = join(root, 'web/public');
  const publicHashes = new Map();
  let omitted = 0;
  let retained = 0;
  for (const station of stations) {
    const source = join(root, 'web/dist', station);
    const manifest = manifestAt(source, station);
    const actual = inventory(source).filter((path) => path !== manifestName).sort();
    if (JSON.stringify(actual) !== JSON.stringify(Object.keys(manifest.files).sort())) throw new Error(`The ${station} build file list differs from its manifest.`);
    for (const [path, expected] of Object.entries(manifest.files)) {
      const bytes = fileBytes(source, path);
      if (hash(bytes) !== expected) throw new Error(`Build hash mismatch: ${station}/${path}`);
      const publicPath = checkedPath(publicRoot, path);
      if (!publicHashes.has(path)) publicHashes.set(path, existsSync(publicPath) ? hash(fileBytes(publicRoot, path)) : null);
      if (publicHashes.get(path) === expected) omitted += 1;
      else {
        put(join(target, station), path, bytes);
        retained += 1;
      }
    }
    put(join(target, station), manifestName, manifest.bytes);
  }
  return { omitted, retained };
}

export function restoreStationBuild(repository, input) {
  const root = realpathSync(repository);
  const source = artifactDirectory(root, input);
  const target = join(root, 'web/dist');
  requireEmpty(root, target);
  const publicRoot = join(root, 'web/public');
  const publicHashes = new Map();
  const manifests = new Map(stations.map((station) => [station, manifestAt(join(source, station), station)]));
  const allowed = new Set(stations.flatMap((station) => [manifestName, ...Object.keys(manifests.get(station).files)].map((path) => `${station}/${path}`)));
  for (const path of inventory(source)) {
    if (!allowed.has(path)) throw new Error(`Artifact contains an unlisted file: ${path}`);
  }
  let restored = 0;
  for (const station of stations) {
    const packed = join(source, station);
    const manifest = manifests.get(station);
    for (const [path, expected] of Object.entries(manifest.files)) {
      const packedPath = checkedPath(packed, path);
      const destination = checkedPath(join(target, station), path);
      if (existsSync(packedPath)) {
        const bytes = fileBytes(packed, path);
        if (hash(bytes) !== expected) throw new Error(`Artifact hash mismatch: ${station}/${path}`);
        put(join(target, station), path, bytes);
      } else {
        if (!publicHashes.has(path)) publicHashes.set(path, hash(fileBytes(publicRoot, path)));
        if (publicHashes.get(path) !== expected) throw new Error(`Public input hash mismatch: ${station}/${path}`);
        mkdirSync(dirname(destination), { recursive: true });
        copyFileSync(checkedPath(publicRoot, path), destination);
      }
      restored += 1;
    }
    put(join(target, station), manifestName, manifest.bytes);
  }
  return { restored };
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const [command, path, ...extra] = process.argv.slice(2);
  if (!path || extra.length || !['pack', 'restore'].includes(command)) throw new Error('Usage: node scripts/ci-station-build.mjs pack OUTPUT_DIR | restore INPUT_DIR');
  const result = command === 'pack' ? packStationBuild(process.cwd(), path) : restoreStationBuild(process.cwd(), path);
  console.log(JSON.stringify(result));
}
