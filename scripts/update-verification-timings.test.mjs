import test from 'node:test';
import assert from 'node:assert/strict';
import { raceMeasurements, browserMeasurements, applyRaceMeasurements } from './update-verification-timings.mjs';

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

test('measured whole packages keep valid package weights without unsplit test entries', () => {
  const hints = { split_packages: ['example/split'], package_seconds: { 'example/old': 5 }, test_seconds: { 'example/split': { TestPrior: 3 } } };
  applyRaceMeasurements(hints, { packages: { 'example/split': 12, 'example/whole': 7 }, tests: { 'example/split': { TestNew: 9 }, 'example/whole': { TestWhole: 6 } } });
  assert.deepEqual(hints.package_seconds, { 'example/old': 5, 'example/split': 12, 'example/whole': 7 });
  assert.deepEqual(hints.test_seconds, { 'example/split': { TestPrior: 3, TestNew: 9 } });
});

test('browser reports aggregate nested tests by complete spec file', () => {
  const report = { suites: [{ suites: [{ specs: [{ file: 'test/e2e/example.spec.ts', tests: [{ results: [{ duration: 2_000 }, { duration: 500 }] }] }] }] }] };
  assert.deepEqual(browserMeasurements([report]), { 'test/e2e/example.spec.ts': 2.5 });
});
