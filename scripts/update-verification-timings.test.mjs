import test from 'node:test';
import assert from 'node:assert/strict';
import { raceMeasurements, goMeasurements, browserMeasurements, applyRaceMeasurements } from './update-verification-timings.mjs';

test('race timing uses top-level tests with their buffered package context', () => {
  const result = raceMeasurements([`raceplan: output example/a group 1/2
--- PASS: TestTop (12.50s)
    --- PASS: TestTop/sub (11.0s)
ok  example/a 14.0s
raceplan: output example/b
--- SKIP: TestPlatform (0.00s)
ok  example/b 1.0s
`, `raceplan: output example/a group 2/2
--- PASS: TestOther (2.0s)
ok  example/a 3.0s
`]);
  assert.deepEqual(result.packages, { 'example/a': 17, 'example/b': 1 });
  assert.deepEqual(result.tests['example/a'], { TestTop: 12.5, TestOther: 2 });
});

test('measured packages retain weights for implicit and future test splitting', () => {
  const hints = { split_packages: ['example/split'], package_seconds: { 'example/old': 5 }, test_seconds: { 'example/split': { TestPrior: 3 } } };
  applyRaceMeasurements(hints, { packages: { 'example/split': 12, 'example/whole': 7 }, tests: { 'example/split': { TestNew: 9 }, 'example/whole': { TestWhole: 6 } } });
  assert.deepEqual(hints.package_seconds, { 'example/old': 5, 'example/split': 12, 'example/whole': 7 });
  assert.deepEqual(hints.test_seconds, { 'example/split': { TestPrior: 3, TestNew: 9 }, 'example/whole': { TestWhole: 6 } });
});

test('browser reports aggregate nested tests by complete spec file', () => {
  const report = { suites: [{ suites: [{ specs: [{ file: 'test/e2e/example.spec.ts', tests: [{ results: [{ duration: 2_000 }, { duration: 500 }] }] }] }] }] };
  assert.deepEqual(browserMeasurements([report]), { 'test/e2e/example.spec.ts': 2.5 });
});

test('ordinary Go measurements keep complete durations and ignore cached or failed results', () => {
  const logs = [
    'Go tests started at 2026-10-21T14:00:30Z\nok  example/slow 205.13s\nok  example/cached (cached)\nFAIL example/bad 2.0s\n? example/empty [no test files]\n',
    '2026-10-21T14:10:54Z ok example/slow 2.715s\nok example/other 0.001s\n',
  ];
  assert.deepEqual(goMeasurements(logs), { 'example/slow': 205.13, 'example/other': 0.001 });
});
