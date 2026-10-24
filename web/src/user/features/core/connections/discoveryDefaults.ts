import type { DiscoveryFormat, DiscoveryPreset } from '@shared/connections';

const formats: Record<string, DiscoveryFormat> = {
  'openai-compatible': 'openai',
  'anthropic-compatible': 'anthropic',
  'google-gemini': 'gemini',
  'ai-sdk-gateway-v3': 'ai-sdk-gateway-v3',
  'ai-sdk-gateway-v4': 'ai-sdk-gateway-v4',
};

export function defaultDiscovery(
  configuration: string | undefined,
  baseURL: string,
): DiscoveryPreset | null {
  const format = configuration ? formats[configuration] : undefined;
  return format ? { base_url: baseURL.trim(), list_format: format } : null;
}
