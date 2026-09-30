import assert from 'node:assert/strict';
import test from 'node:test';
import { registeredCopyReferences, validateJSONSource } from './check-copy.mjs';

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
