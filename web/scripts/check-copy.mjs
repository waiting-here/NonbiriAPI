import { readFile, readdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import ts from 'typescript';
import dynamicCopyKeys from './copy-dynamic-keys.mjs';

const webRoot = resolve(fileURLToPath(new URL('..', import.meta.url)));

const catalogPairs = [
  ['common', 'src/shared/i18n/common/en.json', 'src/shared/i18n/common/zh.json'],
  ['user', 'src/user/i18n/en.json', 'src/user/i18n/zh.json'],
  ['admin', 'src/admin/i18n/en.json', 'src/admin/i18n/zh.json'],
];

const requiredNavigationKeys = new Map([
  ['user', ['user.report.nav', 'user.announcements.nav']],
  ['admin', ['admin.activities.nav', 'admin.reports.nav', 'admin.announcements.nav', 'admin.mainstreamChannels.nav']],
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

// The reverse gate needs to inspect source literals without treating comments
// as usage. This intentionally remains a small lexer: it only recognizes
// comments and quoted strings/templates, and does not attempt to parse TS.
function stripComments(source) {
  const chars = [...source];
  let state = 'code';
  let quote = '';
  for (let index = 0; index < chars.length; index += 1) {
    const current = chars[index];
    const next = chars[index + 1];
    if (state === 'line-comment') {
      if (current === '\n' || current === '\r') state = 'code';
      else chars[index] = ' ';
      continue;
    }
    if (state === 'block-comment') {
      if (current === '*' && next === '/') {
        chars[index] = ' ';
        chars[index + 1] = ' ';
        index += 1;
        state = 'code';
      } else if (current !== '\n' && current !== '\r') {
        chars[index] = ' ';
      }
      continue;
    }
    if (state === 'string') {
      if (current === '\\') {
        index += 1;
        continue;
      }
      if (current === quote) state = 'code';
      continue;
    }
    if (current === '/' && next === '/') {
      chars[index] = ' ';
      chars[index + 1] = ' ';
      index += 1;
      state = 'line-comment';
    } else if (current === '/' && next === '*') {
      chars[index] = ' ';
      chars[index + 1] = ' ';
      index += 1;
      state = 'block-comment';
    } else if (current === "'" || current === '"' || current === '`') {
      state = 'string';
      quote = current;
    }
  }
  return chars.join('');
}

function stringLiteralValues(source) {
  const values = [];
  const sourceFile = ts.createSourceFile(
    'copy-check.tsx',
    source,
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TSX,
  );
  function visit(node) {
    if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) values.push(node.text);
    ts.forEachChild(node, visit);
  }
  visit(sourceFile);
  return values;
}

const exactCatalogKeyPattern = /^[A-Za-z0-9_.-]+$/;
const dynamicEvidencePattern = /\$\{|\[[A-Za-z_$][\w$]*\]|\b(?:charityCopyKey|charityStatusKey|charityStateKey|reviewerRoleKey|sourceTypeKey|tokenPriceCopyKey)\s*\(/;
const tokenPriceLeaves = new Set([
  'uncached_user_price_milli',
  'cache_write_user_price_milli',
  'cache_read_user_price_milli',
  'output_user_price_milli',
  'uncached_donor_reward_milli',
  'cache_write_donor_reward_milli',
  'cache_read_donor_reward_milli',
  'output_donor_reward_milli',
]);
const donationStatuses = new Set(['pending', 'approved', 'rejected', 'deleted', 'expired']);

function dynamicAnchorKeyError(catalog, key, anchor) {
  const catalogPrefixes = {
    common: ['common.', 'logs.'],
    user: ['user.', 'games.'],
    admin: ['admin.'],
  };
  if (!(catalogPrefixes[catalog] ?? []).some((prefix) => key.startsWith(prefix))) {
    return `dynamic copy manifest key is outside the ${catalog} catalog namespace: ${key}`;
  }

  const template = anchor.match(/`([^`]*)`/u)?.[1] ?? anchor;
  const interpolationIndex = template.indexOf('${');
  if (interpolationIndex >= 0) {
    const prefix = template.slice(0, interpolationIndex);
    if (prefix && !key.startsWith(prefix)) {
      return `dynamic copy manifest key does not match anchor prefix ${prefix}: ${key}`;
    }
  }

  const helper = anchor.match(/\b(charityCopyKey|charityStatusKey|charityStateKey|reviewerRoleKey|sourceTypeKey|tokenPriceCopyKey)\b/u)?.[1];
  if (!helper) return null;
  if (helper === 'charityCopyKey' || helper === 'charityStatusKey' || helper === 'tokenPriceCopyKey') {
    const rolePrefix = catalog === 'admin' ? 'admin.charity.' : catalog === 'user' ? 'user.steward.' : null;
    if (!rolePrefix || !key.startsWith(rolePrefix)) return `dynamic copy manifest role helper does not match catalog ${catalog}: ${key}`;
    if (helper === 'charityStatusKey') {
      const suffix = key.slice(`${rolePrefix}status.`.length);
      if (!key.startsWith(`${rolePrefix}status.`) || !donationStatuses.has(suffix)) {
        return `dynamic copy manifest status helper has an invalid status leaf: ${key}`;
      }
    } else if (helper === 'tokenPriceCopyKey') {
      if (!tokenPriceLeaves.has(key.slice(rolePrefix.length))) {
        return `dynamic copy manifest token-price helper has an invalid leaf: ${key}`;
      }
    } else {
      const staticField = anchor.match(/,\s*'([^']+)'\s*\)/u)?.[1];
      if (staticField && key !== `${rolePrefix}${staticField}`) {
        return `dynamic copy manifest helper field does not match anchor: ${key}`;
      }
      if (anchor.includes("decision === 'approve'")) {
        if (!['approve', 'reject'].includes(key.slice(rolePrefix.length))) {
          return `dynamic copy manifest decision helper has an invalid leaf: ${key}`;
        }
      }
      if (anchor.includes('model.enabled')) {
        if (!['enabled', 'disabled'].includes(key.slice(rolePrefix.length))) {
          return `dynamic copy manifest enabled helper has an invalid leaf: ${key}`;
        }
      }
    }
  } else if (helper === 'charityStateKey') {
    if (!/^common\.operations\.charity\.charityState\.(pending|available|disabled|suspended|exhausted|expired|ended)$/u.test(key)) {
      return `dynamic copy manifest charity-state helper has an invalid leaf: ${key}`;
    }
  } else if (helper === 'reviewerRoleKey') {
    if (!/^common\.operations\.charity\.role\.(admin|steward)$/u.test(key)) {
      return `dynamic copy manifest reviewer-role helper has an invalid leaf: ${key}`;
    }
  } else if (helper === 'sourceTypeKey') {
    if (!/^common\.operations\.charity\.sourceType\.(automatic|manual)$/u.test(key)) {
      return `dynamic copy manifest source-type helper has an invalid leaf: ${key}`;
    }
  }
  return null;
}

function normalizedSourcePath(source) {
  return source.replaceAll('\\', '/');
}

function rememberFailure(failures, error) {
  failures.push(error instanceof Error ? error.message : String(error));
}

const availableKeys = new Set();
const catalogsByName = new Map();
const failures = [];

for (const [name, enPath, zhPath] of catalogPairs) {
  let en;
  let zh;
  try {
    const [enSource, zhSource] = await Promise.all([
      readFile(resolve(webRoot, enPath), 'utf8'),
      readFile(resolve(webRoot, zhPath), 'utf8'),
    ]);
    en = flatten(JSON.parse(enSource));
    zh = flatten(JSON.parse(zhSource));
  } catch (error) {
    rememberFailure(failures, `${name}: unable to read/flatten catalogs: ${error.message}`);
    continue;
  }
  catalogsByName.set(name, en);
  for (const key of en.keys()) availableKeys.add(key);
  const enKeys = [...en.keys()].sort();
  const zhKeys = [...zh.keys()].sort();
  if (!sameValues(enKeys, zhKeys)) {
    const enOnly = enKeys.filter((key) => !zh.has(key));
    const zhOnly = zhKeys.filter((key) => !en.has(key));
    rememberFailure(
      failures,
      `${name} catalog key mismatch; en-only=${enOnly.join(',') || '-'}; zh-only=${zhOnly.join(',') || '-'}.`,
    );
  }
  for (const key of enKeys) {
    if (!zh.has(key)) continue;
    const enPlaceholders = placeholders(en.get(key));
    const zhPlaceholders = placeholders(zh.get(key));
    if (!sameValues(enPlaceholders, zhPlaceholders)) {
      rememberFailure(
        failures,
        `${name} ${key} placeholder mismatch; en=${enPlaceholders.join(',')}; zh=${zhPlaceholders.join(',')}.`,
      );
    }
  }
  for (const key of requiredNavigationKeys.get(name) ?? []) {
    if (!en.has(key) || !zh.has(key)) rememberFailure(failures, `${name} catalog is missing required navigation key ${key}.`);
  }
  // This deliberately scans visible catalog values only. Wire/schema property
  // names are validated by their own strict DTO tests and are not UI copy.
  try {
    visibleValueChecks(name, 'en', en);
    visibleValueChecks(name, 'zh', zh);
  } catch (error) {
    rememberFailure(failures, error);
  }
  process.stdout.write(`${name}: ${en.size} bilingual leaves\n`);
}

const keyPatterns = [
  /\bt\(\s*(['"])([A-Za-z0-9_.-]+)\1/g,
  /\bi18nKey\s*=\s*(['"])([A-Za-z0-9_.-]+)\1/g,
  /\b(?:fallbackLabelKey|labelKey)\s*:\s*(['"])([A-Za-z0-9_.-]+)\1/g,
];
const files = await sourceFiles(resolve(webRoot, 'src'));
const sourceTextByRelativePath = new Map();
for (const file of files) {
  sourceTextByRelativePath.set(normalizedSourcePath(file.slice(webRoot.length + 1)), await readFile(file, 'utf8'));
}

const usedByCatalog = new Map([...catalogsByName.keys()].map((name) => [name, new Set()]));
function markCatalogKey(key) {
  for (const [name, values] of catalogsByName) {
    if (values.has(key)) usedByCatalog.get(name).add(key);
  }
}

const missingReferences = [];
for (const [relativePath, source] of sourceTextByRelativePath) {
  // Core/game workspaces intentionally use closed copy registries. Preserve
  // the existing source->catalog boundary and do not count their local keys
  // as consumers of the global catalogs.
  if (!source.includes('useCoreCopy') && !source.includes('useGameCopy')) {
    for (const key of stringLiteralValues(source)) {
      if (availableKeys.has(key)) markCatalogKey(key);
    }
  }
  // The core workspace intentionally uses its own closed copy registry; its
  // `t()` references are validated alongside that registry.
  if (source.includes('useCoreCopy')) continue;
  for (const pattern of keyPatterns) {
    for (const match of source.matchAll(pattern)) {
      const key = match[2];
      if (!availableKeys.has(key)) missingReferences.push(`${relativePath}:${key}`);
    }
  }
}
if (missingReferences.length > 0) {
  rememberFailure(failures, `Source references missing catalog keys:\n${missingReferences.sort().join('\n')}`);
} else {
  process.stdout.write('source: all static translation and route-label keys resolve\n');
}

if (!Array.isArray(dynamicCopyKeys)) {
  rememberFailure(failures, 'dynamic copy manifest must export an array');
} else {
  const seenManifestKeys = new Set();
  for (const entry of dynamicCopyKeys) {
    if (!entry || typeof entry !== 'object') {
      rememberFailure(failures, 'dynamic copy manifest contains a non-object entry');
      continue;
    }
    const { catalog, key, source, anchor, reason } = entry;
    if (typeof catalog !== 'string' || !catalogsByName.has(catalog)) {
      rememberFailure(failures, `dynamic copy manifest has unknown catalog: ${String(catalog)}`);
      continue;
    }
    if (typeof key !== 'string' || !exactCatalogKeyPattern.test(key)) {
      rememberFailure(failures, `dynamic copy manifest key must be one exact leaf without wildcards: ${String(key)}`);
      continue;
    }
    const manifestIdentity = `${catalog}:${key}`;
    if (seenManifestKeys.has(manifestIdentity)) {
      rememberFailure(failures, `dynamic copy manifest duplicate: ${manifestIdentity}`);
      continue;
    }
    seenManifestKeys.add(manifestIdentity);
    if (!catalogsByName.get(catalog).has(key)) {
      rememberFailure(failures, `dynamic copy manifest key is absent from ${catalog}: ${key}`);
      continue;
    }
    if (typeof source !== 'string' || source.startsWith('/') || source.includes('..')) {
      rememberFailure(failures, `dynamic copy manifest source must be a relative path: ${String(source)}`);
      continue;
    }
    const relativeSource = normalizedSourcePath(source);
    const sourceText = sourceTextByRelativePath.get(relativeSource);
    if (!sourceText) {
      rememberFailure(failures, `dynamic copy manifest source is not a production source file: ${source}`);
      continue;
    }
    if (typeof anchor !== 'string' || !anchor.trim()) {
      rememberFailure(failures, `dynamic copy manifest anchor is empty: ${manifestIdentity}`);
      continue;
    }
    if (!stripComments(sourceText).includes(anchor)) {
      rememberFailure(failures, `dynamic copy manifest anchor is not live code in ${source}: ${anchor}`);
      continue;
    }
    if (!dynamicEvidencePattern.test(anchor)) {
      rememberFailure(failures, `dynamic copy manifest anchor lacks restricted dynamic evidence: ${manifestIdentity}`);
      continue;
    }
    const anchorKeyError = dynamicAnchorKeyError(catalog, key, anchor);
    if (anchorKeyError) {
      rememberFailure(failures, anchorKeyError);
      continue;
    }
    if (typeof reason !== 'string' || !reason.trim()) {
      rememberFailure(failures, `dynamic copy manifest reason is empty: ${manifestIdentity}`);
      continue;
    }
    usedByCatalog.get(catalog).add(key);
  }
}

const orphaned = [];
for (const [name, values] of catalogsByName) {
  const used = usedByCatalog.get(name);
  for (const key of values.keys()) {
    if (!used.has(key)) orphaned.push(`${name} -> ${key}`);
  }
}
if (orphaned.length) {
  rememberFailure(failures, `Catalog keys defined but not used by production sources (orphan):\n${orphaned.join('\n')}`);
} else {
  process.stdout.write('reverse: every catalog key is used by a production source or an audited dynamic manifest entry\n');
}

if (failures.length) throw new Error(failures.join('\n'));
