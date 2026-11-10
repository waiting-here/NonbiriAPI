import { describe, expect, it } from 'vitest';
import { defaultDiscovery } from './discoveryDefaults';

describe('standard discovery defaults', () => {
  it.each([
    ['openai-compatible', 'openai', '/models'],
    ['openai-responses', 'openai', '/models'],
    ['openai-chat-completions', 'openai', '/models'],
    ['openai-decisions', 'openai', '/models'],
    ['typesafe-systemone', 'openai', '/models'],
    ['anthropic-compatible', 'anthropic', '/models'],
    ['google-gemini', 'gemini', '/models'],
    ['ai-sdk-gateway-v3', 'ai-sdk-gateway-v3', '/config'],
    ['ai-sdk-gateway-v4', 'ai-sdk-gateway-v4', '/config'],
  ])('suggests the complete %s catalog URL', (configuration, format, suffix) => {
    expect(defaultDiscovery(configuration, ' https://provider.example/api/ ')).toEqual({
      base_url: 'https://provider.example/api' + suffix,
      list_format: format,
    });
    const complete = 'https://provider.example/api' + suffix;
    expect(defaultDiscovery(configuration, complete)?.base_url).toBe(complete);
    expect(defaultDiscovery(configuration, complete + '/')?.base_url).toBe(complete + '/');
  });
  it('keeps an empty address ready for input', () => {
    expect(defaultDiscovery('openai-compatible', '')?.base_url).toBe('');
  });
  it('does not guess a catalog protocol for unsupported configurations', () => {
    expect(defaultDiscovery('cohere-rerank', 'https://provider.example')).toBeNull();
    expect(defaultDiscovery('unknown-protocol', 'https://provider.example')).toBeNull();
    expect(defaultDiscovery(undefined, 'https://provider.example')).toBeNull();
  });
});
