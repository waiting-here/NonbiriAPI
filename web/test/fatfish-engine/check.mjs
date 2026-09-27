import { fileURLToPath } from 'node:url';
import { chromium, firefox, webkit } from '@playwright/test';
import { createServer } from 'vite';

const browsers = { chromium, firefox, webkit };
const selected = process.argv.slice(2);
if (!selected.length) selected.push(...Object.keys(browsers));
if (selected.some((name) => !Object.hasOwn(browsers, name))) throw new Error('Unknown browser');
const server = await createServer({
  root: fileURLToPath(new URL('../../', import.meta.url)),
  configFile: false,
  appType: 'custom',
  logLevel: 'warn',
  server: { host: '127.0.0.1', port: 0 },
  optimizeDeps: { noDiscovery: true, include: [], entries: [] },
});
server.middlewares.use((request, response, next) => {
  if (request.url !== '/') return next();
  response.setHeader('Content-Type', 'text/html');
  response.end('<!doctype html><html><title>Deterministic engine verification</title></html>');
});
await server.listen();
try {
  const address = server.httpServer.address();
  for (const name of selected) {
    const browser = await browsers[name].launch({ headless: true });
    try {
      const page = await browser.newPage();
      await page.goto(`http://127.0.0.1:${address.port}/`);
      const result = await page.evaluate(async () => {
        const prefix = '/src/shared/fatfish/engine/';
        const [
          { parseLevel, seedCommit, stateDigest },
          { replay },
          { decodeHex, hex, sha256, utf8 },
          { initialFishRNG, nextTurnBit },
          { SIN_TABLE, TRIG_SOURCE_SHA256 },
          suite,
        ] = await Promise.all([
          import(prefix + 'canonical.ts'),
          import(prefix + 'engine.ts'),
          import(prefix + 'sha256.ts'),
          import(prefix + 'protocol.ts'),
          import(prefix + 'trig.ts'),
          fetch(prefix + 'golden.json').then((value) => value.json()),
        ]);
        const equal = (a, b) => {
          const sorted = (value) =>
            Array.isArray(value)
              ? value.map(sorted)
              : value && typeof value === 'object'
                ? Object.fromEntries(
                    Object.keys(value)
                      .sort()
                      .map((key) => [key, sorted(value[key])]),
                  )
                : value;
          return JSON.stringify(sorted(a)) === JSON.stringify(sorted(b));
        };
        if (
          TRIG_SOURCE_SHA256 !== suite.trig_sha256 ||
          hex(sha256(utf8(`${SIN_TABLE.join('\n')}\n`))) !== suite.trig_sha256
        )
          throw new Error('Trigonometric source changed');
        const first = suite.cases[0],
          seed = decodeHex(first.seed);
        if (
          seedCommit('challenge_1', 'period_1', 'node_1', first.result.content_hash, seed) !==
          suite.seed_commit
        )
          throw new Error('Seed commitment changed');
        const random = initialFishRNG(seed, 1);
        let bits = '';
        for (let i = 0; i < 128; i++) bits += String(nextTurnBit(random));
        if (bits !== suite.turn_bits) throw new Error('Random stream changed');
        let ticks = 0;
        const started = performance.now();
        for (const vector of suite.cases) {
          let tick = 0;
          const actual = replay(
            parseLevel(JSON.stringify(vector.level)),
            decodeHex(vector.seed),
            vector.inputs ?? [],
            {
              onTick(state) {
                if (stateDigest(state) !== vector.hashes[tick])
                  throw new Error(`${vector.name}: divergent tick ${tick}`);
                tick++;
              },
            },
          );
          if (tick !== vector.hashes.length || !equal(actual, vector.result))
            throw new Error(`${vector.name}: divergent result`);
          ticks += tick;
        }
        return {
          cases: suite.cases.length,
          ticks,
          elapsed_ms: Math.round(performance.now() - started),
        };
      });
      console.log(JSON.stringify({ browser: name, version: browser.version(), ...result }));
    } finally {
      await browser.close();
    }
  }
} finally {
  await server.close();
}
