import { spawn } from 'node:child_process';
import { resolve } from 'node:path';
import { once } from 'node:events';

// CI supplies one exact-build test binary; each suite starts its own process,
// state directory and database. Standalone local suites retain their builder.
export async function browserFixtureBinary(root, directory) {
  const shared = process.env.NONBIRI_BROWSER_FIXTURE_BINARY;
  if (shared) {
    return resolve(shared);
  }
  const binary = resolve(directory, process.platform === 'win32' ? 'fixture.exe' : 'fixture');
  const build = spawn(process.env.GO_BINARY || 'go',
    ['test', '-c', '-tags', 'dist', '-o', binary, './internal/app'],
    { cwd: root, env: { ...process.env, CGO_ENABLED: '0' },
      windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] });
  let output = '';
  const collect = (chunk) => { output = (output + chunk).slice(-32768); };
  build.stdout.on('data', collect);
  build.stderr.on('data', collect);
  const timeout = setTimeout(() => build.kill(), 180_000);
  const [code] = await once(build, 'exit').finally(() => clearTimeout(timeout));
  if (code !== 0) throw new Error('Go fixture build failed: ' + code + '\n' + output);
  return binary;
}
