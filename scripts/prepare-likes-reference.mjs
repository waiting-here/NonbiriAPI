import fs from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { createRequire } from 'node:module';

// Compile a read-only copy of the reference package for fixture generation.
// No reference source, package script, browser code, or network code is run.
const [sourceDirectory, outputDirectory] = process.argv.slice(2);
if (!sourceDirectory || !outputDirectory) {
  throw new Error('Usage: prepare-likes-reference.mjs reference-package empty-output-directory');
}
const source = path.resolve(sourceDirectory);
const output = path.resolve(outputDirectory);
if (output === source || output.startsWith(source + path.sep)) {
  throw new Error('Output must be outside the source package');
}
if (fs.existsSync(output) && fs.readdirSync(output).length > 0) {
  throw new Error('Output directory must be empty');
}
const require = createRequire(new URL('../web/package.json', import.meta.url));
const ts = require('typescript');
const core = path.join(source, 'src', 'core');
const hashes = {};
fs.mkdirSync(path.join(output, 'core'), { recursive: true });
fs.mkdirSync(path.join(output, 'data'), { recursive: true });
for (const name of fs.readdirSync(core).sort()) {
  if (!name.endsWith('.ts') || name.endsWith('.test.ts')) continue;
  let text = fs.readFileSync(path.join(core, name), 'utf8');
  hashes[`core/${name}`] = createHash('sha256').update(text).digest('hex');
  if (name === 'simultaneous.ts') {
    const randomHook = 's.rng=x>>>0;return s.rng%length';
    const roundHook = 'function begin(s:Game,c:Config){';
    if (!text.includes(randomHook) || !text.includes(roundHook)) {
      throw new Error('Unexpected reference revision');
    }
    text = text.replace(randomHook, 's.rng=x>>>0;(globalThis as any).__draws?.push({candidate_count:length,index:s.rng%length});return s.rng%length');
    text = text.replace(roundHook, roundHook + '(globalThis as any).__beforeBegin=clone(s);');
  }
  const compiled = ts.transpileModule(text, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS, esModuleInterop: true, resolveJsonModule: true },
  });
  fs.writeFileSync(path.join(output, 'core', name.replace(/\.ts$/, '.js')), compiled.outputText.replaceAll('\r\n', '\n'));
}
const data = fs.readFileSync(path.join(source, 'src', 'data', 'default.json'));
hashes['data/default.json'] = createHash('sha256').update(data).digest('hex');
fs.writeFileSync(path.join(output, 'data', 'default.json'), data);
fs.writeFileSync(path.join(output, 'sources.json'), JSON.stringify(hashes, null, 2) + '\n');
