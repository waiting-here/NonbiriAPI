import { readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { basename, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export function raceMeasurements(logs) {
  const tests = {};
  const packages = {};
  for (const log of logs) {
    let current;
    for (const raw of log.split('\n')) {
      const line = raw.replace(/^\d{4}-\d\d-\d\dT\S+Z /, '');
      const context = /^raceplan: output (\S+)/.exec(line);
      if (context) current = context[1];
      const test = /^--- (?:PASS|SKIP): (\S+) \(([\d.]+)s\)/.exec(line);
      if (test && current) (tests[current] ||= {})[test[1]] = Math.max(0.001, +test[2]);
      const result = /^ok\s+(\S+)\s+([\d.]+)s/.exec(line);
      if (result) packages[result[1]] = (packages[result[1]] || 0) + +result[2];
    }
  }
  return { tests, packages };
}

export function browserMeasurements(reports) {
  const seconds = {};
  function visit(suite) {
    for (const spec of suite.specs || []) {
      const file = spec.file.replaceAll('\\', '/');
      const marker = file.indexOf('test/e2e/');
      const path = marker >= 0 ? file.slice(marker) : `test/e2e/${file}`;
      for (const test of spec.tests || []) {
        for (const result of test.results || []) {
          seconds[path] = (seconds[path] || 0) + result.duration / 1_000;
        }
      }
    }
    for (const child of suite.suites || []) visit(child);
  }
  for (const report of reports) for (const suite of report.suites || []) visit(suite);
  return seconds;
}

function filesIn(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    return entry.isDirectory() ? filesIn(path) : entry.isFile() ? [path] : [];
  });
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const [directory, runURL, commit, attempt = '1'] = process.argv.slice(2);
  if (!directory || !/^https:\/\/[^/]+\/[^/]+\/[^/]+\/actions\/runs\/\d+$/.test(runURL || '') || !/^[a-f0-9]{40}$/.test(commit || '') || !/^[1-9]\d*$/.test(attempt)) {
    throw new Error('Usage: node scripts/update-verification-timings.mjs ARTIFACT_DIRECTORY RUN_URL COMMIT [ATTEMPT]');
  }
  const root = resolve(import.meta.dirname, '..');
  const files = filesIn(directory);
  const race = raceMeasurements(files.filter((path) => /^race-.*\.log$/.test(basename(path))).map((path) => readFileSync(path, 'utf8')));
  const browser = browserMeasurements(files.filter((path) => /^browser-smoke-.*\.json$/.test(basename(path))).map((path) => JSON.parse(readFileSync(path, 'utf8'))));
  if (!Object.keys(race.packages).length && !Object.keys(browser).length) throw new Error('No usable test measurements found.');
  if (Object.keys(race.packages).length) {
    const path = join(root, 'scripts/race-timings.json');
    const hints = JSON.parse(readFileSync(path, 'utf8'));
    Object.assign(hints.package_seconds, race.packages);
    for (const [pkg, tests] of Object.entries(race.tests)) Object.assign(hints.test_seconds[pkg] ||= {}, tests);
    hints.source = {
      run_url: runURL, commit, attempt: +attempt,
      measured_packages: Object.keys(race.packages).length,
      measured_tests: Object.values(race.tests).reduce((sum, tests) => sum + Object.keys(tests).length, 0),
      note: 'Observed risk-race test and package durations. Unobserved entries retain prior estimates; live discovery defines coverage.',
    };
    writeFileSync(path, `${JSON.stringify(hints, null, 2)}\n`);
  }
  if (Object.keys(browser).length) {
    const path = join(root, 'web/test/e2e-timings.json');
    const hints = JSON.parse(readFileSync(path, 'utf8'));
    Object.assign(hints.spec_seconds, browser);
    hints.source = { run_url: runURL, commit, attempt: +attempt, measurement: 'Playwright test durations; balancing hints only.' };
    writeFileSync(path, `${JSON.stringify(hints, null, 2)}\n`);
  }
  console.log(`Updated hints from ${Object.keys(race.packages).length} packages and ${Object.keys(browser).length} browser specs.`);
}
