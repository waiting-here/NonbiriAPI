import type { Page } from '@playwright/test';
import type { NativeServiceOption } from '../../src/shared/connections';
import { numberedPage } from './numbered-fixtures';
import { USER_ORIGIN } from './ports';
export const nativeOptions: NativeServiceOption[] = [
  {
    connector_type: 'openai-compatible',
    configuration_type: 'openai-compatible',
    native_services: ['chat_completions', 'responses', 'decisions', 'embeddings'],
    default_native_services: ['chat_completions', 'responses'],
    rerank_formats: [],
  },
  {
    connector_type: 'anthropic-compatible',
    configuration_type: 'anthropic-compatible',
    native_services: ['anthropic_messages'],
    default_native_services: ['anthropic_messages'],
    rerank_formats: [],
  },
  {
    connector_type: 'google-gemini',
    configuration_type: 'google-gemini',
    native_services: ['gemini_generate_content'],
    default_native_services: ['gemini_generate_content'],
    rerank_formats: [],
  },
  {
    connector_type: 'typesafe-systemone',
    configuration_type: 'typesafe-systemone',
    native_services: ['systemone'],
    default_native_services: ['systemone'],
    rerank_formats: [],
  },
  ...['cohere', 'jina', 'siliconflow', 'voyage']
    .map((type) => ({
      connector_type: type + '-rerank',
      configuration_type: type + '-rerank',
      native_services: ['rerank'] as const,
      default_native_services: ['rerank'] as const,
      rerank_formats: [],
    }))
    .map((value) => ({
      ...value,
      native_services: [...value.native_services],
      default_native_services: [...value.default_native_services],
    })),
  ...['v3', 'v4'].map((version) => ({
    connector_type: 'ai-sdk-gateway-' + version,
    configuration_type: 'ai-sdk-gateway-' + version,
    native_services: [
      'gateway_text',
      'images_generations',
      'embeddings',
      'rerank',
    ] as NativeServiceOption['native_services'],
    default_native_services: ['gateway_text'] as NativeServiceOption['native_services'],
    rerank_formats: [],
  })),
];
nativeOptions.push(
  ...['openai-responses', 'openai-chat-completions', 'openai-decisions'].map((connector_type) => ({
    ...nativeOptions[0]!,
    connector_type,
  })),
);
export const createOptions = {
  base_connector_types: nativeOptions.map((option) => option.connector_type),
  mainstream_channels: [],
  native_service_options: nativeOptions,
};
export const connection = {
  id: '1',
  name: 'Browser connection',
  note: '',
  revision: '1',
  entry_count: '1',
  credential_count: '1',
  created_at: 1700000000,
  updated_at: 1700000001,
};
export const credential = {
  id: '31',
  connection_id: '1',
  note: 'Shared credential',
  enabled: true,
  revision: '1',
  credential_version: '1',
  display_head: 'head',
  display_tail: 'tail',
  created_at: 1700000000,
  updated_at: 1700000001,
};
export async function mockConnectionResources(
  page: Page,
  entries: () => Record<string, unknown>[] = () => [],
  connections: () => Record<string, unknown>[] = () => [connection],
) {
  await page.route(`${USER_ORIGIN}/api/connections**`, async (route) => {
    const request = route.request(),
      url = new URL(request.url());
    if (url.pathname === '/api/connections') {
      if (request.method() === 'POST')
        return route.fulfill({ status: 201, json: { ...connection, ...request.postDataJSON() } });
      return route.fulfill({ json: numberedPage(connections(), url.searchParams) });
    }
    if (url.pathname === '/api/connections/1') return route.fulfill({ json: connection });
    if (url.pathname === '/api/connections/1/entries')
      return route.fulfill({ json: numberedPage(entries(), url.searchParams) });
    if (url.pathname === '/api/connections/1/credentials')
      return route.fulfill({
        status: request.method() === 'POST' ? 201 : 200,
        json:
          request.method() === 'POST'
            ? { ...credential, note: request.postDataJSON().note }
            : numberedPage([credential], url.searchParams),
      });
    if (url.pathname === '/api/connections/1/discovery-sources')
      return route.fulfill({ json: numberedPage([], url.searchParams) });
    return route.fallback();
  });
}

export function mixedMainstreamLayout(input: Record<string, unknown>) {
  return (
    Object.hasOwn(input, 'entries') &&
    (Object.hasOwn(input, 'connector_type') || Object.hasOwn(input, 'base_url'))
  );
}
export function mainstreamProjection(input: Record<string, unknown>) {
  const first = Array.isArray(input.entries)
    ? (input.entries[0] as Record<string, unknown> | undefined)
    : undefined;
  return first ? { connector_type: first.connector_type, base_url: first.base_url } : {};
}
