export function supportsChatStoragePolicy(connector: string): boolean {
  return connector === 'openai-compatible' || connector === 'openai-chat-completions';
}
