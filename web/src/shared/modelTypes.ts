import { array, invalidResponse, oneOf } from './operations/wire';

export const MODEL_TYPES = [
  'chat_completions',
  'embeddings',
  'images_generations',
  'decisions',
  'responses',
  'responses_compact',
  'responses_input_tokens',
  'completions',
  'images_edits',
  'images_variations',
  'audio_speech',
  'audio_transcriptions',
  'audio_translations',
  'moderations',
  'rerank',
  'systemone',
] as const;
export type ModelType = (typeof MODEL_TYPES)[number];

export const MODEL_TYPE_PATHS: Record<ModelType, string> = {
  chat_completions: '/v1/chat/completions',
  embeddings: '/v1/embeddings',
  images_generations: '/v1/images/generations',
  decisions: '/v1/decisions',
  responses: '/v1/responses',
  responses_compact: '/v1/responses/compact',
  responses_input_tokens: '/v1/responses/input_tokens',
  completions: '/v1/completions',
  images_edits: '/v1/images/edits',
  images_variations: '/v1/images/variations',
  audio_speech: '/v1/audio/speech',
  audio_transcriptions: '/v1/audio/transcriptions',
  audio_translations: '/v1/audio/translations',
  moderations: '/v1/moderations',
  rerank: '/v1/rerank',
  systemone: '/v1/systemone',
};

export function normalizeModelTypes(value: unknown): ModelType[] {
  const types = array(value, 'model types', MODEL_TYPES.length).map((type) =>
    oneOf(type, MODEL_TYPES, 'model type'),
  );
  if (types.length === 0 || new Set(types).size !== types.length) invalidResponse('model types');
  return types;
}

export function sameModelTypes(left: readonly ModelType[], right: readonly ModelType[]): boolean {
  const a = new Set(left);
  const b = new Set(right);
  return a.size === b.size && [...a].every((type) => b.has(type));
}

export const MODEL_TYPE_LABEL_KEYS: Record<ModelType, string> = {
  chat_completions: 'common.modelTypes.chat_completions',
  embeddings: 'common.modelTypes.embeddings',
  images_generations: 'common.modelTypes.images_generations',
  decisions: 'common.modelTypes.decisions',
  responses: 'common.modelTypes.responses',
  responses_compact: 'common.modelTypes.responses_compact',
  responses_input_tokens: 'common.modelTypes.responses_input_tokens',
  completions: 'common.modelTypes.completions',
  images_edits: 'common.modelTypes.images_edits',
  images_variations: 'common.modelTypes.images_variations',
  audio_speech: 'common.modelTypes.audio_speech',
  audio_transcriptions: 'common.modelTypes.audio_transcriptions',
  audio_translations: 'common.modelTypes.audio_translations',
  moderations: 'common.modelTypes.moderations',
  rerank: 'common.modelTypes.rerank',
  systemone: 'common.modelTypes.systemone',
};

export const MODEL_TYPE_CHOICES = MODEL_TYPES;
export function modelTypeGroup(type: ModelType): ModelType {
  return type;
}
export const MODEL_TYPE_GROUP_LABEL_KEYS = MODEL_TYPE_LABEL_KEYS;
export function groupedModelTypes(value: readonly ModelType[]): ModelType[] {
  return MODEL_TYPE_CHOICES.filter((type) => value.some((item) => modelTypeGroup(item) === type));
}
export function setModelTypeGroup(
  value: readonly ModelType[],
  type: ModelType,
  enabled: boolean,
): ModelType[] {
  const groups = new Set(value.map(modelTypeGroup));
  if (enabled) groups.add(modelTypeGroup(type));
  else groups.delete(modelTypeGroup(type));
  return MODEL_TYPES.filter((item) => groups.has(modelTypeGroup(item)));
}
export function modelTypeGroupPath(type: ModelType): string {
  return MODEL_TYPE_PATHS[type];
}
