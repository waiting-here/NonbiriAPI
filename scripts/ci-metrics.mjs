import { appendFileSync, mkdirSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

function seconds(start, end) {
  const value = (Date.parse(end) - Date.parse(start)) / 1_000;
  return Number.isFinite(value) && value >= 0 ? value : null;
}

export function summarizeJobs(jobs) {
  return jobs.filter((job) => job.completed_at).map((job) => {
    const stages = { setup: 0, build: 0, test: 0, other: 0 };
    for (const step of job.steps || []) {
      const duration = seconds(step.started_at, step.completed_at);
      if (duration === null || step.conclusion === 'skipped') continue;
      let stage = 'other';
      if (/test|browser contracts|typecheck|lint|upgrade populated|race shard/i.test(step.name)) stage = 'test';
      else if (/build|compile|prepare live catalog/i.test(step.name)) stage = 'build';
      else if (/set up|check out|install|download|pin npm|post /i.test(step.name)) stage = 'setup';
      stages[stage] += duration;
    }
    return {
      name: job.name, conclusion: job.conclusion,
      queued_seconds: seconds(job.created_at, job.started_at),
      execution_seconds: seconds(job.started_at, job.completed_at),
      stages,
      steps: (job.steps || []).map((step) => ({ name: step.name, conclusion: step.conclusion, seconds: seconds(step.started_at, step.completed_at) })),
    };
  }).sort((a, b) => (b.execution_seconds || 0) - (a.execution_seconds || 0));
}

export function markdownSummary(jobs) {
  const duration = (value) => value === null ? 'unavailable' : `${Math.round(value)}s`;
  const runnerMinutes = jobs.reduce((sum, job) => sum + (job.execution_seconds || 0), 0) / 60;
  return [
    '## Verification timing', '',
    `Completed jobs used ${runnerMinutes.toFixed(1)} runner-minutes. Queue time is reported separately; the current summary job is not included. Combined build/vet/test commands appear under checks; the JSON retains individual step names and durations.`, '',
    '| Job | Result | Queue | Execution | Setup | Build | Checks |',
    '|---|---|---:|---:|---:|---:|---:|',
    ...jobs.map((job) => `| ${job.name.replaceAll('|', '\\|')} | ${job.conclusion} | ${duration(job.queued_seconds)} | ${duration(job.execution_seconds)} | ${duration(job.stages.setup)} | ${duration(job.stages.build)} | ${duration(job.stages.test)} |`), '',
  ].join('\n');
}

async function fetchJobs() {
  const { GITHUB_REPOSITORY: repository, GITHUB_RUN_ID: run, GITHUB_TOKEN: token } = process.env;
  if (!repository || !run || !token) throw new Error('GitHub run identity and read token are required.');
  const jobs = [];
  for (let page = 1; ; page += 1) {
    const response = await fetch(`${process.env.GITHUB_API_URL || 'https://api.github.com'}/repos/${repository}/actions/runs/${run}/jobs?per_page=100&page=${page}`, {
      headers: { Accept: 'application/vnd.github+json', Authorization: `Bearer ${token}` },
      signal: AbortSignal.timeout(30_000),
    });
    if (!response.ok) throw new Error(`Job metadata request failed (${response.status}).`);
    const data = await response.json();
    jobs.push(...data.jobs);
    if (data.jobs.length < 100) return jobs;
  }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const jobs = summarizeJobs(await fetchJobs());
  const report = { format_version: 1, commit: process.env.GITHUB_SHA, run: process.env.GITHUB_RUN_ID, jobs };
  mkdirSync('.cache/ci', { recursive: true });
  writeFileSync('.cache/ci/metrics.json', `${JSON.stringify(report, null, 2)}\n`);
  const markdown = markdownSummary(jobs);
  if (process.env.GITHUB_STEP_SUMMARY) appendFileSync(process.env.GITHUB_STEP_SUMMARY, markdown);
  else process.stdout.write(markdown);
}
