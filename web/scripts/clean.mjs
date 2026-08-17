// Removes the build output directory (web/dist).
import { rm } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const webDir = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const distDir = resolve(webDir, 'dist');

await rm(distDir, { recursive: true, force: true });
console.log(`cleaned ${distDir}`);
