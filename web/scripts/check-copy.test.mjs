import assert from 'node:assert/strict';
import test from 'node:test';
import { registeredCopyReferences, validateJSONSource, visibleValueChecks } from './check-copy.mjs';

test('JSON authority rejects duplicate nested keys and CRLF', () => {
  assert.throws(
    () => validateJSONSource('catalog.json', '{"common":{"save":"Save","save":"Store"}}'),
    /duplicate JSON key save/,
  );
  assert.throws(() => validateJSONSource('catalog.json', '{\r\n"save":"Save"\r\n}'), /LF/);
  assert.doesNotThrow(() =>
    validateJSONSource('catalog.json', '{"common":{"save":"Save"},"user":{"save":"Save"}}'),
  );
});

test('closed aliases resolve to the root authority and reject hidden or dynamic word lists', () => {
  const available = new Set(['common.save']);
  const code = "const keys = { save: 'common.save' } as const; useRegisteredCopy(keys);";
  assert.deepEqual(registeredCopyReferences('copy.ts', code, available), ['common.save']);
  assert.throws(
    () =>
      registeredCopyReferences('copy.ts', "useRegisteredCopy({save:'common.missing'})", available),
    /missing root key/,
  );
  assert.throws(
    () => registeredCopyReferences('copy.ts', 'useRegisteredCopy(remoteKeys)', available),
    /closed literal alias map/,
  );
  assert.throws(
    () =>
      registeredCopyReferences(
        'copy.ts',
        "useRegisteredCopy({...keys,save:'common.save'})",
        available,
      ),
    /point directly/,
  );
  assert.throws(
    () =>
      registeredCopyReferences(
        'copy.ts',
        "useRegisteredCopy({save:'common.save',save:'common.save'})",
        available,
      ),
    /duplicate copy alias/,
  );
  assert.throws(
    () =>
      registeredCopyReferences('copy.ts', "useRegisteredCopy({[alias]:'common.save'})", available),
    /point directly/,
  );
});

test('user terminology gate rejects ordinary technical labels in both languages', () => {
  for (const value of [
    '端点',
    '平台模型',
    'store=false',
    'CallerKey',
    '透传',
    '假流式',
    '假非流',
    '扁平化',
    'JSON Pointer',
    'RFC 6901',
  ]) {
    assert.throws(
      () => visibleValueChecks('user', 'zh', new Map([['user.core.field', value]])),
      /technical terminology/,
    );
  }
  for (const value of [
    'Endpoint',
    'platform model',
    'CallerKey',
    'passthrough',
    'flatten',
    'JSON Pointer',
  ]) {
    assert.throws(
      () => visibleValueChecks('user', 'en', new Map([['user.core.field', value]])),
      /technical terminology/,
    );
  }
});

test('user terminology gate allows ordinary wording and scoped technical explanations', () => {
  assert.doesNotThrow(() =>
    visibleValueChecks(
      'user',
      'zh',
      new Map([
        ['user.core.field', '服务商的模型名'],
        ['user.core.storeHelp', 'store=false'],
        ['user.personalAutomation.identity', 'CallerKey'],
        ['user.debug.result', '端点'],
        ['user.legal.body', '端点'],
      ]),
    ),
  );
  assert.doesNotThrow(() =>
    visibleValueChecks(
      'user',
      'en',
      new Map([
        ['user.core.field', 'Provider model name {{endpoint}}'],
        ['user.personalAutomation.identity', 'CallerKey'],
        ['user.debug.result', 'Endpoint'],
        ['user.legal.body', 'Endpoint'],
      ]),
    ),
  );
  assert.doesNotThrow(() =>
    visibleValueChecks('common', 'zh', new Map([['common.adminHelp', '端点']])),
  );
  assert.doesNotThrow(() =>
    visibleValueChecks('admin', 'en', new Map([['admin.field', 'Endpoint']])),
  );
  assert.throws(
    () =>
      visibleValueChecks('user', 'zh', new Map([['user.personalAutomationExtra.field', '端点']])),
    /technical terminology/,
  );
  assert.throws(
    () => visibleValueChecks('user', 'zh', new Map([['user.core.storePolicy', 'store=false']])),
    /technical terminology/,
  );
});
