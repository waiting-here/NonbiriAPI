import { describe, expect, it } from 'vitest';
import { defaultDiscovery } from './discoveryDefaults';

describe('standard discovery defaults', () => {
  it.each([
    ['openai-compatible', 'openai'],
    ['openai-responses', 'openai'],
    ['openai-chat-completions', 'openai'],
    ['openai-decisions', 'openai'],
    ['typesafe-systemone', 'openai'],
    ['anthropic-compatible', 'anthropic'],
    ['google-gemini', 'gemini'],
    ['ai-sdk-gateway-v3', 'ai-sdk-gateway-v3'],
    ['ai-sdk-gateway-v4', 'ai-sdk-gateway-v4'],
  ])('uses the existing %s catalog format', (configuration, format) => {
    expect(defaultDiscovery(configuration, 'https://provider.example/api')).toEqual({
      base_url: 'https://provider.example/api',
      list_format: format,
    });
  });
  it('does not guess a catalog protocol for unsupported configurations', () => {
    expect(defaultDiscovery('cohere-rerank', 'https://provider.example')).toBeNull();
    expect(defaultDiscovery('unknown-protocol', 'https://provider.example')).toBeNull();
    expect(defaultDiscovery(undefined, 'https://provider.example')).toBeNull();
  });
});
