export const MODEL_CALL_ROUTES = [
  'openai_chat_completions',
  'charity_chat_completions',
  'openai_embeddings',
  'charity_embeddings',
  'openai_images_generations',
  'charity_images_generations',
  'openai_decisions',
  'charity_decisions',
  'openai_responses',
  'charity_responses',
  'openai_responses_compact',
  'charity_responses_compact',
  'openai_responses_input_tokens',
  'charity_responses_input_tokens',
  'openai_completions',
  'charity_completions',
  'openai_images_edits',
  'charity_images_edits',
  'openai_images_variations',
  'charity_images_variations',
  'openai_audio_speech',
  'charity_audio_speech',
  'openai_audio_transcriptions',
  'charity_audio_transcriptions',
  'openai_audio_translations',
  'charity_audio_translations',
  'openai_moderations',
  'charity_moderations',
  'openai_rerank',
  'charity_rerank',
  'openai_systemone',
  'charity_systemone',
] as const;

export type ModelCallRoute = (typeof MODEL_CALL_ROUTES)[number];

export function isCharityRoute(
  route: string,
): route is Extract<ModelCallRoute, `charity_${string}`> {
  return MODEL_CALL_ROUTES.some((value) => value === route && value.startsWith('charity_'));
}

export function isEmbeddingRoute(
  route: string,
): route is 'openai_embeddings' | 'charity_embeddings' {
  return route === 'openai_embeddings' || route === 'charity_embeddings';
}

export function isDecisionRoute(route: string): route is 'openai_decisions' | 'charity_decisions' {
  return route === 'openai_decisions' || route === 'charity_decisions';
}
