export const MODEL_CALL_ROUTES = [
  'openai_chat_completions',
  'charity_chat_completions',
  'openai_embeddings',
  'charity_embeddings',
] as const;

export type ModelCallRoute = (typeof MODEL_CALL_ROUTES)[number];

export function isCharityRoute(
  route: string,
): route is 'charity_chat_completions' | 'charity_embeddings' {
  return route === 'charity_chat_completions' || route === 'charity_embeddings';
}

export function isEmbeddingRoute(
  route: string,
): route is 'openai_embeddings' | 'charity_embeddings' {
  return route === 'openai_embeddings' || route === 'charity_embeddings';
}
