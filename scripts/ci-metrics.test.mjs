import test from 'node:test';
import assert from 'node:assert/strict';
import { summarizeJobs, markdownSummary } from './ci-metrics.mjs';

test('execution cost excludes queue and unfinished jobs, and missing timestamps stay unknown', () => {
  const jobs = summarizeJobs([
    { name: 'Web browser', conclusion: 'success', created_at: '2026-10-12T00:00:00Z', started_at: '2026-10-12T00:01:00Z', completed_at: '2026-10-12T00:03:00Z', steps: [
      { name: 'Install dependencies', started_at: '2026-10-12T00:01:00Z', completed_at: '2026-10-12T00:01:10Z' },
      { name: 'Selected browser contracts', started_at: '2026-10-12T00:01:10Z', completed_at: '2026-10-12T00:03:00Z' },
    ] },
    { name: 'Go', conclusion: 'failure', started_at: '2026-10-12T00:01:00Z', completed_at: '2026-10-12T00:02:00Z' },
    { name: 'Unfinished', started_at: '2026-10-12T00:00:00Z' },
  ]);
  assert.equal(jobs.length, 2);
  assert.equal(jobs[0].queued_seconds, 60);
  assert.equal(jobs[0].execution_seconds, 120);
  assert.deepEqual(jobs[0].stages, { setup: 10, build: 0, test: 110, other: 0 });
  assert.equal(jobs[1].queued_seconds, null);
  assert.match(markdownSummary(jobs), /3\.0 runner-minutes/);
  assert.match(markdownSummary(jobs), /unavailable/);
});
