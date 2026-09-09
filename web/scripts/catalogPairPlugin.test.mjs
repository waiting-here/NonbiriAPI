import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import catalogPairPlugin, {
  CATALOG_PAIRS,
  packCatalogPair,
  pairVirtualId,
  renderPairModuleSource,
  renderWrapperModuleSource,
  wrapperVirtualId,
} from './catalogPairPlugin.mjs';

const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

function readJson(relativePath) {
  return JSON.parse(fs.readFileSync(path.join(webRoot, relativePath), 'utf8'));
}

function dataModuleUrl(source, suffix = '') {
  return `data:text/javascript;base64,${Buffer.from(source, 'utf8').toString('base64')}${suffix}`;
}

function sourcePair(name) {
  const definition = CATALOG_PAIRS.find((candidate) => candidate.name === name);
  assert.ok(definition, `missing catalog definition ${name}`);
  return [readJson(definition.en), readJson(definition.zh)];
}

test('production plugin intercepts only exact catalog JSON imports', () => {
  const plugin = catalogPairPlugin({ webRoot });
  const importer = path.join(webRoot, 'src/user/i18n/index.ts');
  assert.equal(plugin.apply, 'build');
  assert.equal(plugin.resolveId('./zh.json', importer), wrapperVirtualId('user', 1));
  assert.equal(plugin.resolveId('./zh.json?raw', importer), null);
  assert.equal(plugin.resolveId('./unrelated.json', importer), null);
  assert.equal(plugin.resolveId('./zh.json', undefined), null);
});

test('all six resources restore every value and original key order', async () => {
  const plugin = catalogPairPlugin({ webRoot });
  let importNumber = 0;
  for (const definition of CATALOG_PAIRS) {
    const [en, zh] = sourcePair(definition.name);
    const pairSource = plugin.load(pairVirtualId(definition.name));
    assert.equal(typeof pairSource, 'string');
    const pairModule = await import(dataModuleUrl(pairSource, `#pair-${(importNumber += 1)}`));
    assert.equal(JSON.stringify(pairModule.default[0]), JSON.stringify(en), definition.name);
    assert.equal(JSON.stringify(pairModule.default[1]), JSON.stringify(zh), definition.name);

    for (const [language, resource] of [en, zh].entries()) {
      const wrapperId = wrapperVirtualId(definition.name, language);
      const wrapperSource = plugin.load(wrapperId);
      assert.equal(typeof wrapperSource, 'string');
      const wrapperPairSource = wrapperSource.replace(
        JSON.stringify(pairVirtualId(definition.name)),
        JSON.stringify(dataModuleUrl(pairSource)),
      );
      const wrapperModule = await import(
        dataModuleUrl(wrapperPairSource, `#wrapper-${(importNumber += 1)}`)
      );
      assert.equal(
        JSON.stringify(wrapperModule.default),
        JSON.stringify(resource),
        `${definition.name}/${language}`,
      );
      for (const key of Object.keys(resource)) {
        if (/^(?:[$_\p{ID_Start}])(?:[$_\u200C\u200D\p{ID_Continue}])*$/u.test(key)) {
          assert.equal(
            JSON.stringify(wrapperModule[key]),
            JSON.stringify(resource[key]),
            `${definition.name}/${language}/${key}`,
          );
        }
      }
    }
  }
});

test('same-order leaves, empty objects and reordered objects use safe JSON semantics', async () => {
  const en = JSON.parse(
    '{"normal":"quote \\" slash \\\\ unicode 雪 ${template} {{token}}","empty":{},"reordered":{"first":"EN first","second":"EN second","__proto__":{"own":"EN nested proto"}},"__proto__":{"own":"EN proto"}}',
  );
  const zh = JSON.parse(
    '{"normal":"引号 \\" 反斜杠 \\\\ Unicode 雪 ${template} {{token}}","empty":{},"reordered":{"second":"ZH second","first":"ZH first","__proto__":{"own":"ZH nested proto"}},"__proto__":{"own":"ZH proto"}}',
  );
  const packed = packCatalogPair(en, zh);
  assert.deepEqual(Object.keys(packed), Object.keys(en));
  assert.equal(Array.isArray(packed.empty), false);
  assert.equal(Object.hasOwn(packed, '__proto__'), true);
  assert.equal(Object.getPrototypeOf(packed), Object.prototype);
  const reordered = packed.reordered;
  assert.equal(JSON.stringify(reordered[0]), JSON.stringify(en.reordered));
  assert.equal(JSON.stringify(reordered[1]), JSON.stringify(zh.reordered));

  const module = await import(dataModuleUrl(renderPairModuleSource('fixture', en, zh), '#fixture'));
  assert.equal(JSON.stringify(module.default[0]), JSON.stringify(en));
  assert.equal(JSON.stringify(module.default[1]), JSON.stringify(zh));
  assert.equal(Object.hasOwn(module.default[0], '__proto__'), true);
  assert.equal(module.default[0].__proto__.own, 'EN proto');
  assert.equal(Object.hasOwn(module.default[0].reordered, '__proto__'), true);
  assert.equal(module.default[0].reordered.__proto__.own, 'EN nested proto');
  assert.equal(Object.getPrototypeOf(module.default[0]), Object.prototype);
});

test('catalog shape mismatches are rejected before packing', () => {
  assert.throws(
    () => packCatalogPair({ one: 'en' }, { two: 'zh' }),
    /catalog key set mismatch at <root>/u,
  );
  assert.throws(
    () => packCatalogPair({ one: {} }, { one: 'zh' }),
    /catalog shape mismatch at one/u,
  );
  assert.throws(() => packCatalogPair(['en'], ['zh']), /catalog shape mismatch at <root>/u);
});

test('wrapper emits only legal top-level named exports', () => {
  const resource = JSON.parse(
    '{"ok":"yes","default":"reserved","await":"reserved","enum":"reserved","arguments":"reserved","__proto__":"proto","with-hyphen":"bad"}',
  );
  const source = renderWrapperModuleSource('fixture', 0, resource);
  assert.match(source, /export \{ __catalogNamedExport0 as ok \};/u);
  assert.match(source, /export \{ __catalogNamedExport1 as __proto__ \};/u);
  assert.match(source, /export \{ __catalogResource as default \};/u);
  assert.doesNotMatch(source, /export const/u);
  assert.doesNotMatch(source, /as await/u);
  assert.doesNotMatch(source, /as enum/u);
  assert.doesNotMatch(source, /as arguments/u);
  assert.doesNotMatch(source, /with-hyphen/u);
});

test('wrapper keeps internal bindings separate from colliding public names', async () => {
  const resource = JSON.parse(
    '{"resource":"resource value","pair":"pair value","__catalogPair":"pair alias","__catalogResource":"resource alias","__catalogNamedExport0":"counter alias","__proto__":"proto value"}',
  );
  const pairSource = renderPairModuleSource('fixture', resource, resource);
  const wrapperSource = renderWrapperModuleSource('fixture', 0, resource).replace(
    JSON.stringify(pairVirtualId('fixture')),
    JSON.stringify(dataModuleUrl(pairSource)),
  );
  const module = await import(dataModuleUrl(wrapperSource, '#binding-collisions'));
  assert.equal(module.default.resource, 'resource value');
  assert.equal(module.default.pair, 'pair value');
  assert.equal(module.resource, 'resource value');
  assert.equal(module.pair, 'pair value');
  assert.equal(module.__catalogPair, 'pair alias');
  assert.equal(module.__catalogResource, 'resource alias');
  assert.equal(module.__catalogNamedExport0, 'counter alias');
  assert.equal(module.__proto__, 'proto value');
});

test('a legal __proto__ named export remains executable', async () => {
  const resource = JSON.parse('{"ok":"yes","__proto__":"proto"}');
  const pairSource = `const pair = JSON.parse(${JSON.stringify(JSON.stringify([resource, resource]))});\nexport default pair;\n`;
  const wrapperSource = renderWrapperModuleSource('fixture', 0, resource).replace(
    JSON.stringify(pairVirtualId('fixture')),
    JSON.stringify(dataModuleUrl(pairSource)),
  );
  const module = await import(dataModuleUrl(wrapperSource, '#proto-named-export'));
  assert.equal(module.__proto__, 'proto');
  assert.equal(module.ok, 'yes');
});
