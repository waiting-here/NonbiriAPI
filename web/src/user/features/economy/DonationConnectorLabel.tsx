import { ConnectorLabel } from '../core/components';

export function DonationConnectorLabel({ value }: { value: string }) {
  if (
    value === 'openai-compatible' ||
    value === 'anthropic-compatible' ||
    value === 'ai-sdk-gateway-v3' ||
    value === 'ai-sdk-gateway-v4' ||
    value === 'google-gemini' ||
    value === 'openai-responses' ||
    value === 'openai-decisions' ||
    value === 'cohere-rerank' ||
    value === 'jina-rerank' ||
    value === 'siliconflow-rerank' ||
    value === 'voyage-rerank' ||
    value === 'typesafe-systemone' ||
    value === 'openai-chat-completions'
  ) {
    return <ConnectorLabel value={value} />;
  }
  return <>{value}</>;
}
