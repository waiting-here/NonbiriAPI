import { ConnectorLabel } from '../core/components';

export function DonationConnectorLabel({ value }: { value: string }) {
  if (
    value === 'openai-compatible' ||
    value === 'anthropic-compatible' ||
    value === 'ai-sdk-gateway-v3'
  ) {
    return <ConnectorLabel value={value} />;
  }
  return <>{value}</>;
}
