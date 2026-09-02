import { readFile, readdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const webRoot = resolve(fileURLToPath(new URL('..', import.meta.url)));

const catalogPairs = [
  ['common', 'src/shared/i18n/common/en.json', 'src/shared/i18n/common/zh.json'],
  ['user', 'src/user/i18n/en.json', 'src/user/i18n/zh.json'],
  ['admin', 'src/admin/i18n/en.json', 'src/admin/i18n/zh.json'],
];

const requiredNavigationKeys = new Map([
  ['user', ['user.report.nav', 'user.announcements.nav']],
  ['admin', ['admin.activities.nav', 'admin.reports.nav', 'admin.announcements.nav']],
]);

function flatten(value, prefix = '', result = new Map()) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error(`Catalog branch ${prefix || '<root>'} must be an object.`);
  }
  for (const [key, child] of Object.entries(value)) {
    const path = prefix ? `${prefix}.${key}` : key;
    if (child && typeof child === 'object' && !Array.isArray(child)) {
      flatten(child, path, result);
      continue;
    }
    if (typeof child !== 'string') {
      throw new Error(`Catalog leaf ${path} must be a string.`);
    }
    result.set(path, child);
  }
  return result;
}

function placeholders(value) {
  return [...value.matchAll(/{{\s*([A-Za-z][A-Za-z0-9_]*)\s*}}/g)]
    .map((match) => match[1])
    .sort();
}

function sameValues(left, right) {
  return left.length === right.length && left.every((value, index) => value === right[index]);
}

function visibleValueChecks(catalog, language, entries) {
  const forbidden = /model_fetch_failed|default_locale|_milli/i;
  for (const [key, value] of entries) {
    if (forbidden.test(value)) {
      throw new Error(`${catalog}/${language} ${key} exposes a forbidden internal identifier.`);
    }
    if (language === 'en') {
      const withoutReservedPrefix = value.replaceAll('[公益]', '');
      if (/[\u3400-\u9fff]/u.test(withoutReservedPrefix)) {
        throw new Error(`${catalog}/en ${key} contains unexpected Chinese copy.`);
      }
    }
    if (catalog === 'user' && /milli-credits?|毫积分/i.test(value)) {
      throw new Error(`${catalog}/${language} ${key} exposes an internal accounting unit.`);
    }
  }
}

async function sourceFiles(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const result = [];
  for (const entry of entries) {
    const path = resolve(directory, entry.name);
    if (entry.isDirectory()) result.push(...await sourceFiles(path));
    else if (/\.(?:ts|tsx)$/.test(entry.name) && !/\.(?:test|spec)\.(?:ts|tsx)$/.test(entry.name)) result.push(path);
  }
  return result;
}

const availableKeys = new Set();

for (const [name, enPath, zhPath] of catalogPairs) {
  const [enSource, zhSource] = await Promise.all([
    readFile(resolve(webRoot, enPath), 'utf8'),
    readFile(resolve(webRoot, zhPath), 'utf8'),
  ]);
  const en = flatten(JSON.parse(enSource));
  const zh = flatten(JSON.parse(zhSource));
  for (const key of en.keys()) availableKeys.add(key);
  const enKeys = [...en.keys()].sort();
  const zhKeys = [...zh.keys()].sort();
  if (!sameValues(enKeys, zhKeys)) {
    const enOnly = enKeys.filter((key) => !zh.has(key));
    const zhOnly = zhKeys.filter((key) => !en.has(key));
    throw new Error(`${name} catalog key mismatch; en-only=${enOnly.join(',') || '-'}; zh-only=${zhOnly.join(',') || '-'}.`);
  }
  for (const key of enKeys) {
    const enPlaceholders = placeholders(en.get(key));
    const zhPlaceholders = placeholders(zh.get(key));
    if (!sameValues(enPlaceholders, zhPlaceholders)) {
      throw new Error(`${name} ${key} placeholder mismatch; en=${enPlaceholders.join(',')}; zh=${zhPlaceholders.join(',')}.`);
    }
  }
  for (const key of requiredNavigationKeys.get(name) ?? []) {
    if (!en.has(key) || !zh.has(key)) throw new Error(`${name} catalog is missing required navigation key ${key}.`);
  }
  // This deliberately scans visible catalog values only. Wire/schema property
  // names are validated by their own strict DTO tests and are not UI copy.
  visibleValueChecks(name, 'en', en);
  visibleValueChecks(name, 'zh', zh);
  process.stdout.write(`${name}: ${en.size} bilingual leaves\n`);
}

const keyPatterns = [
  /\bt\(\s*(['"])([A-Za-z0-9_.-]+)\1/g,
  /\bi18nKey\s*=\s*(['"])([A-Za-z0-9_.-]+)\1/g,
  /\b(?:fallbackLabelKey|labelKey)\s*:\s*(['"])([A-Za-z0-9_.-]+)\1/g,
];
const missingReferences = [];
for (const path of await sourceFiles(resolve(webRoot, 'src'))) {
  const source = await readFile(path, 'utf8');
  // The core workspaces intentionally use their own closed copy registry;
  // their `t()` references are validated alongside that registry.
  if (source.includes('useCoreCopy')) continue;
  for (const pattern of keyPatterns) {
    for (const match of source.matchAll(pattern)) {
      const key = match[2];
      if (!availableKeys.has(key)) {
        missingReferences.push(`${path.slice(webRoot.length + 1)}:${key}`);
      }
    }
  }
}
if (missingReferences.length > 0) {
  throw new Error(`Source references missing catalog keys:\n${missingReferences.sort().join('\n')}`);
}
process.stdout.write('source: all static translation and route-label keys resolve\n');
