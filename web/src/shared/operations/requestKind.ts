export const MODEL_CALL_ROUTES = [
  'openai_chat_completions',
  'charity_chat_completions',
  'openai_embeddings',
  'charity_embeddings',
  'openai_images_generations',
  'charity_images_generations',
  'openai_decisions',
  'charity_decisions',
] as const;

export type ModelCallRoute = (typeof MODEL_CALL_ROUTES)[number];

export function isCharityRoute(
  route: string,
): route is
  | 'charity_chat_completions'
  | 'charity_embeddings'
  | 'charity_images_generations'
  | 'charity_decisions' {
  return (
    route === 'charity_chat_completions' ||
    route === 'charity_embeddings' ||
    route === 'charity_images_generations' ||
    route === 'charity_decisions'
  );
}

export function isEmbeddingRoute(
  route: string,
): route is 'openai_embeddings' | 'charity_embeddings' {
  return route === 'openai_embeddings' || route === 'charity_embeddings';
}

export function isDecisionRoute(route: string): route is 'openai_decisions' | 'charity_decisions' {
  return route === 'openai_decisions' || route === 'charity_decisions';
}
