import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const SCRIPT_DIRECTORY = path.dirname(fileURLToPath(import.meta.url));
const DEFAULT_WEB_ROOT = path.resolve(SCRIPT_DIRECTORY, '..');

export const CATALOG_PAIRS = [
  {
    name: 'common',
    en: 'src/shared/i18n/common/en.json',
    zh: 'src/shared/i18n/common/zh.json',
  },
  {
    name: 'user',
    en: 'src/user/i18n/en.json',
    zh: 'src/user/i18n/zh.json',
  },
  {
    name: 'admin',
    en: 'src/admin/i18n/en.json',
    zh: 'src/admin/i18n/zh.json',
  },
];

const PAIR_PREFIX = '\0nonbiri-catalog-pair:';
const WRAPPER_PREFIX = '\0nonbiri-catalog-wrapper:';
const IDENTIFIER_PATTERN = /^(?:[$_\p{ID_Start}])(?:[$_\u200C\u200D\p{ID_Continue}])*$/u;
const RESERVED_NAMED_EXPORTS = new Set([
  'arguments',
  'await',
  'break',
  'case',
  'catch',
  'class',
  'const',
  'continue',
  'debugger',
  'default',
  'delete',
  'do',
  'else',
  'enum',
  'export',
  'extends',
  'false',
  'finally',
  'for',
  'function',
  'if',
  'implements',
  'import',
  'in',
  'interface',
  'instanceof',
  'let',
  'new',
  'null',
  'package',
  'private',
  'protected',
  'public',
  'return',
  'static',
  'super',
  'switch',
  'this',
  'throw',
  'true',
  'try',
  'typeof',
  'var',
  'void',
  'while',
  'with',
  'yield',
  'eval',
]);

function isObject(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function pathLabel(currentPath) {
  return currentPath.join('.') || '<root>';
}

function sameKeySet(left, right) {
  if (left.length !== right.length) return false;
  const rightKeys = new Set(right);
  return left.every((key) => rightKeys.has(key));
}

function sameKeyOrder(left, right) {
  return left.length === right.length && left.every((key, index) => key === right[index]);
}

/**
 * The source catalogs must have the same object/string shape and key set.
 * Individual object nodes may have a different order; those nodes are kept as
 * a language pair instead of being recursively re-keyed.
 */
export function assertCatalogShape(en, zh, currentPath = []) {
  if (typeof en === 'string' && typeof zh === 'string') return;
  if (!isObject(en) || !isObject(zh)) {
    throw new Error(`catalog shape mismatch at ${pathLabel(currentPath)}`);
  }
  const enKeys = Object.keys(en);
  const zhKeys = Object.keys(zh);
  if (!sameKeySet(enKeys, zhKeys)) {
    throw new Error(`catalog key set mismatch at ${pathLabel(currentPath)}`);
  }
  for (const key of enKeys) assertCatalogShape(en[key], zh[key], [...currentPath, key]);
}

/**
 * Pack same-order objects recursively. A node whose key order differs is
 * intentionally retained as [enObject, zhObject], so decoder selection keeps
 * both the original order and every original value. Object.fromEntries keeps
 * the source order while defining __proto__ as an own data property.
 */
export function packCatalogPair(en, zh, currentPath = []) {
  assertCatalogShape(en, zh, currentPath);
  if (typeof en === 'string') return [en, zh];
  const enKeys = Object.keys(en);
  const zhKeys = Object.keys(zh);
  if (!sameKeyOrder(enKeys, zhKeys)) return [en, zh];
  return Object.fromEntries(
    enKeys.map((key) => [key, packCatalogPair(en[key], zh[key], [...currentPath, key])]),
  );
}

const DECODER_SOURCE = `function decode(node, language) {
  if (Array.isArray(node)) return node[language];
  return Object.fromEntries(
    Object.entries(node).map(([key, value]) => [key, decode(value, language)]),
  );
}`;

function readCatalogPairs(webRoot) {
  const pairs = new Map();
  for (const definition of CATALOG_PAIRS) {
    const enPath = path.resolve(webRoot, definition.en);
    const zhPath = path.resolve(webRoot, definition.zh);
    const enValue = JSON.parse(fs.readFileSync(enPath, 'utf8'));
    const zhValue = JSON.parse(fs.readFileSync(zhPath, 'utf8'));
    assertCatalogShape(enValue, zhValue);
    const record = {
      ...definition,
      enPath,
      zhPath,
      enValue,
      zhValue,
    };
    pairs.set(definition.name, record);
  }
  return pairs;
}

function isLegalNamedExport(key) {
  return IDENTIFIER_PATTERN.test(key) && !RESERVED_NAMED_EXPORTS.has(key);
}

export function pairVirtualId(name) {
  return `${PAIR_PREFIX}${name}`;
}

export function wrapperVirtualId(name, language) {
  return `${WRAPPER_PREFIX}${name}:${language}`;
}

export function renderPairModuleSource(name, enValue, zhValue) {
  const packed = packCatalogPair(enValue, zhValue);
  // Parsing a JSON string, instead of embedding object literals directly,
  // preserves JSON's own-property semantics for keys such as "__proto__".
  const packedJson = JSON.stringify(JSON.stringify(packed));
  return `const packed = JSON.parse(${packedJson});\n${DECODER_SOURCE}\nconst resources = [decode(packed, 0), decode(packed, 1)];\nexport { resources };\nexport default resources;\n`;
}

export function renderWrapperModuleSource(name, language, resource) {
  const named = Object.keys(resource)
    .filter(isLegalNamedExport)
    .map(
      (key, index) =>
        `const __catalogNamedExport${index} = __catalogResource[${JSON.stringify(key)}];\nexport { __catalogNamedExport${index} as ${key} };`,
    )
    .join('\n');
  return `import __catalogPair from ${JSON.stringify(pairVirtualId(name))};\nconst __catalogResource = __catalogPair[${language}];\nexport { __catalogResource as default };\n${named}\n`;
}

export default function catalogPairPlugin({ webRoot = DEFAULT_WEB_ROOT } = {}) {
  const projectRoot = path.resolve(webRoot);
  const pairs = readCatalogPairs(projectRoot);
  const pairByPath = new Map();
  for (const pair of pairs.values()) {
    pairByPath.set(path.normalize(pair.enPath), { pair, language: 0 });
    pairByPath.set(path.normalize(pair.zhPath), { pair, language: 1 });
  }

  return {
    name: 'nonbiri-catalog-pair',
    apply: 'build',
    enforce: 'pre',
    resolveId(source, importer) {
      if (source.startsWith(PAIR_PREFIX) || source.startsWith(WRAPPER_PREFIX)) return source;
      if (!importer || !source.endsWith('.json') || source.includes('?') || source.includes('#'))
        return null;
      const resolved = path.normalize(path.resolve(path.dirname(importer), source));
      const match = pairByPath.get(resolved);
      return match ? wrapperVirtualId(match.pair.name, match.language) : null;
    },
    load(id) {
      if (id.startsWith(PAIR_PREFIX)) {
        const name = id.slice(PAIR_PREFIX.length);
        const pair = pairs.get(name);
        if (!pair) throw new Error(`unknown catalog pair ${name}`);
        return renderPairModuleSource(name, pair.enValue, pair.zhValue);
      }
      if (id.startsWith(WRAPPER_PREFIX)) {
        const [name, language] = id.slice(WRAPPER_PREFIX.length).split(':');
        const pair = pairs.get(name);
        if (!pair || (language !== '0' && language !== '1'))
          throw new Error(`unknown catalog wrapper ${id}`);
        const resource = language === '0' ? pair.enValue : pair.zhValue;
        return renderWrapperModuleSource(name, language, resource);
      }
      return null;
    },
  };
}
