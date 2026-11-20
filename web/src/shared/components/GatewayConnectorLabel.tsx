import { useTranslation } from 'react-i18next';

const CONNECTOR_LABEL_KEYS: Record<string, string> = {
  'openai-compatible': 'common.connectorOpenaiCompatible',
  'anthropic-compatible': 'common.connectorAnthropicCompatible',
  'ai-sdk-gateway-v3': 'common.gatewayName',
  'ai-sdk-gateway-v4': 'common.gatewayV4Name',
  'google-gemini': 'common.connectorGemini',
  'openai-responses': 'common.connectorResponses',
  'openai-decisions': 'common.connectorDecisions',
  'cohere-rerank': 'common.connectorRerank.cohere',
  'jina-rerank': 'common.connectorRerank.jina',
  'siliconflow-rerank': 'common.connectorRerank.siliconflow',
  'voyage-rerank': 'common.connectorRerank.voyage',
  'typesafe-systemone': 'common.connectorSystemOne',
  'openai-chat-completions': 'common.connectorChatCompletions',
};

export function GatewayConnectorLabel({ value }: { value: string | null | undefined }) {
  const { t } = useTranslation();
  const key = value ? CONNECTOR_LABEL_KEYS[value] : undefined;
  return <>{key ? t(key) : value}</>;
}
