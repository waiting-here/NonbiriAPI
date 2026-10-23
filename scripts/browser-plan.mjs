import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

export const integrationGroups = {
  content: ['picturebook', 'fatfish', 'lakenotes'],
  management: ['audit', 'management', 'workflow', 'proxy'],
};

export function selectSpecs(specs, shard, hints = {}) {
  const unique = [...new Set(specs)].sort();
  if (shard === 'all') return unique;
  const match = /^(\d+)\/(\d+)$/.exec(shard);
  if (!match || +match[1] < 1 || +match[1] > +match[2] || +match[2] > 16) {
    throw new Error('Expected a bounded shard such as 1/2.');
  }
  const count = +match[2];
  const weight = (spec) => {
    const seconds = hints.spec_seconds?.[spec];
    return Number.isFinite(seconds) && seconds > 0 ? seconds : hints.default_spec_seconds || 10;
  };
  const bins = Array.from({ length: count }, () => ({ seconds: 0, specs: [] }));
  for (const spec of unique.sort((a, b) => weight(b) - weight(a) || a.localeCompare(b))) {
    const target = bins.reduce((best, bin) => bin.seconds < best.seconds ? bin : best);
    target.specs.push(spec);
    target.seconds += weight(spec);
  }
  return bins[+match[1] - 1].specs.sort();
}

export function selectSuites(suites, group) {
  const allowed = Object.values(integrationGroups).flat();
  if (suites.some((suite) => !allowed.includes(suite))) throw new Error('Unknown browser integration suite.');
  if (group === 'all') return allowed.filter((suite) => suites.includes(suite));
  if (!integrationGroups[group]) throw new Error('Unknown integration group.');
  return integrationGroups[group].filter((suite) => suites.includes(suite));
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const plan = JSON.parse(process.env.BROWSER_PLAN || 'null');
  if (!plan) throw new Error('BROWSER_PLAN is required; use the shared verification planner.');
  const [kind, selection = 'all'] = process.argv.slice(2);
  let values;
  if (kind === 'specs') {
    const hints = JSON.parse(readFileSync(new URL('../web/test/e2e-timings.json', import.meta.url), 'utf8'));
    values = selectSpecs(plan.specs || [], selection, hints);
  } else if (kind === 'suites') values = selectSuites(plan.suites || [], selection);
  else if (kind === 'related') values = plan.related || [];
  else if (kind === 'engine') values = [plan.engine ? 'true' : 'false'];
  else throw new Error('Unknown browser plan projection.');
  for (const value of values) process.stdout.write(`${value}\0`);
}
