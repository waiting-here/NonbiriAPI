import { spawn } from 'node:child_process';
import { access, mkdir, readFile, unlink, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { once } from 'node:events';
import { setTimeout as delay } from 'node:timers/promises';

export default async function setup() {
  const root = resolve('..');
  const statePath = process.env.NONBIRI_IMAGE_BROWSER_STATE;
  if (!statePath) throw new Error('The picturebook state path is missing.');
  for (const station of ['user', 'admin']) {
    await access(resolve('dist', station, 'index.html'));
  }
  await mkdir(dirname(statePath), { recursive: true });
  await unlink(statePath).catch((error) => {
    if (error.code !== 'ENOENT') throw error;
  });
  const binary = resolve(
    dirname(statePath),
    process.platform === 'win32' ? 'fixture.exe' : 'fixture',
  );
  const go = process.env.GO_BINARY || 'go';
  const build = spawn(go, ['test', '-c', '-tags', 'dist', '-o', binary, '.'], {
    cwd: root,
    env: { ...process.env, CGO_ENABLED: '0' },
    windowsHide: true,
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let buildOutput = '';
  const collectBuild = (chunk) => {
    buildOutput = (buildOutput + chunk).slice(-32768);
  };
  build.stdout.on('data', collectBuild);
  build.stderr.on('data', collectBuild);
  const buildTimeout = setTimeout(() => build.kill(), 180_000);
  const [buildCode] = await once(build, 'exit').finally(() => clearTimeout(buildTimeout));
  if (buildCode !== 0)
    throw new Error('Go fixture build failed: ' + buildCode + '\n' + buildOutput);
  const child = spawn(
    binary,
    ['-test.run=^TestImageBrowserFixture$', '-test.v', '-test.timeout=9m'],
    {
      cwd: root,
      env: { ...process.env, NONBIRI_IMAGE_BROWSER_STATE: statePath },
      windowsHide: true,
      stdio: ['ignore', 'pipe', 'pipe'],
    },
  );
  let output = '';
  const collect = (chunk) => {
    output = (output + chunk).slice(-65536);
  };
  child.stdout.on('data', collect);
  child.stderr.on('data', collect);
  const exited = once(child, 'exit');
  let state;
  try {
    const deadline = Date.now() + 60_000;
    while (Date.now() < deadline) {
      if (child.exitCode !== null) throw new Error('The Go fixture stopped:\n' + output);
      try {
        state = JSON.parse(await readFile(statePath, 'utf8'));
        break;
      } catch (error) {
        if (error.code !== 'ENOENT' && !(error instanceof SyntaxError)) throw error;
      }
      await delay(100);
    }
    if (!state) throw new Error('The Go fixture did not become ready:\n' + output);
  } catch (error) {
    child.kill();
    await exited;
    await writeFile(resolve(dirname(statePath), 'fixture.log'), output);
    throw error;
  }
  return async () => {
    let shutdownError;
    try {
      const response = await fetch(state.control_url + '/shutdown', {
        method: 'POST',
        headers: {
          Authorization: 'Bearer ' + state.control_token,
          'Content-Type': 'application/json',
        },
        body: '{}',
        signal: AbortSignal.timeout(5000),
      });
      if (!response.ok) throw new Error('Fixture shutdown returned ' + response.status);
    } catch (error) {
      shutdownError = error;
    }
    const kill = setTimeout(() => child.kill(), 10_000);
    const [code] = await exited.finally(() => clearTimeout(kill));
    await writeFile(resolve(dirname(statePath), 'fixture.log'), output);
    if (code !== 0) throw new Error('The Go fixture failed: ' + code + '\n' + output);
    if (shutdownError) throw shutdownError;
  };
}
