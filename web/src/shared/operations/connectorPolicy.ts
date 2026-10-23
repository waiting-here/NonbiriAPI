export function supportsChatStoragePolicy(connector: string): boolean {
  return connector === 'openai-compatible' || connector === 'openai-chat-completions';
}

export function isAISDKGateway(connector: string | undefined): boolean {
  return connector === 'ai-sdk-gateway-v3' || connector === 'ai-sdk-gateway-v4';
}
