import { describe, expect, it } from 'vitest';
import { discoveryError } from './discoveryError';

describe('image discovery failure guidance', () => {
  it('distinguishes transport failure from invalid catalog data in both languages', () => {
    const en = (_zh: string, english: string) => english;
    const zh = (chinese: string) => chinese;
    expect(discoveryError('upstream_failed', undefined, en)).toContain('TLS certificates');
    expect(discoveryError('upstream_failed', undefined, zh)).toContain('连接失败类别');
    expect(discoveryError('invalid_result', 200, en)).toContain(
      'catalog format may be unsupported',
    );
    expect(discoveryError('invalid_result', 200, en)).not.toMatch(/JSON|pointer|adapter/);
    expect(discoveryError('upstream_failed', 404, en)).not.toMatch(/discovery.path|adapter/);
    expect(discoveryError('upstream_failed', 401, en)).toContain('permission to list models');
    expect(discoveryError('execution_timeout', undefined, en)).toContain('timed out');
  });
});
