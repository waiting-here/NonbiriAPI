import { describe, expect, it } from 'vitest';
import { discoveryError } from './discoveryError';

describe('image discovery failure guidance', () => {
  it('distinguishes transport failure from invalid catalog data in both languages', () => {
    const en = (_zh: string, english: string) => english;
    const zh = (chinese: string) => chinese;
    expect(discoveryError('upstream_failed', undefined, en)).toContain('TLS certificates');
    expect(discoveryError('upstream_failed', undefined, zh)).toContain('连接失败类别');
    expect(discoveryError('invalid_result', 200, en)).toContain('discovery.items_pointer');
    expect(discoveryError('upstream_failed', 401, en)).toContain('permission to list models');
    expect(discoveryError('execution_timeout', undefined, en)).toContain('timed out');
  });
});
