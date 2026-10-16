import { ConnectorLabel } from '../core/components';

export function DonationConnectorLabel({ value }: { value: string }) {
  if (
    value === 'openai-compatible' ||
    value === 'anthropic-compatible' ||
    value === 'ai-sdk-gateway-v3' ||
    value === 'google-gemini' ||
    value === 'openai-responses' ||
    value === 'openai-decisions'
  ) {
    return <ConnectorLabel value={value} />;
  }
  return <>{value}</>;
}
