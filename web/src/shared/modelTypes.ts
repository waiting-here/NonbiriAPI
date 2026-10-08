import { array, invalidResponse, oneOf } from './operations/wire';

export const MODEL_TYPES = ['chat_completions', 'embeddings', 'images_generations'] as const;
export type ModelType = (typeof MODEL_TYPES)[number];

export const MODEL_TYPE_PATHS: Record<ModelType, string> = {
  chat_completions: '/v1/chat/completions',
  embeddings: '/v1/embeddings',
  images_generations: '/v1/images/generations',
};

export function normalizeModelTypes(value: unknown): ModelType[] {
  const types = array(value, 'model types', MODEL_TYPES.length).map((type) =>
    oneOf(type, MODEL_TYPES, 'model type'),
  );
  if (types.length === 0 || new Set(types).size !== types.length) invalidResponse('model types');
  return types;
}

export function sameModelTypes(left: readonly ModelType[], right: readonly ModelType[]): boolean {
  return left.length === right.length && left.every((type) => right.includes(type));
}
