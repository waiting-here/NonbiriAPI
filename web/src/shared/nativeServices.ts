export const NATIVE_SERVICES = [
  'chat_completions',
  'responses',
  'responses_compact',
  'responses_input_tokens',
  'embeddings',
  'images_generations',
  'images_edits',
  'images_variations',
  'audio_speech',
  'audio_transcriptions',
  'audio_translations',
  'completions',
  'moderations',
  'decisions',
  'rerank',
  'systemone',
  'anthropic_messages',
  'gemini_generate_content',
  'gateway_text',
] as const;

export type NativeService = (typeof NATIVE_SERVICES)[number];

export const NATIVE_SERVICE_LABEL_KEYS: Record<NativeService, string> = {
  chat_completions: 'common.modelTypes.chat_completions',
  responses: 'common.modelTypes.responses',
  responses_compact: 'common.modelTypes.responses_compact',
  responses_input_tokens: 'common.modelTypes.responses_input_tokens',
  embeddings: 'common.modelTypes.embeddings',
  images_generations: 'common.modelTypes.images_generations',
  images_edits: 'common.modelTypes.images_edits',
  images_variations: 'common.modelTypes.images_variations',
  audio_speech: 'common.modelTypes.audio_speech',
  audio_transcriptions: 'common.modelTypes.audio_transcriptions',
  audio_translations: 'common.modelTypes.audio_translations',
  completions: 'common.modelTypes.completions',
  moderations: 'common.modelTypes.moderations',
  decisions: 'common.modelTypes.decisions',
  rerank: 'common.modelTypes.rerank',
  systemone: 'common.modelTypes.systemone',
  anthropic_messages: 'common.nativeServices.anthropic_messages',
  gemini_generate_content: 'common.nativeServices.gemini_generate_content',
  gateway_text: 'common.nativeServices.gateway_text',
};

export const RERANK_FORMATS = ['generic', 'cohere', 'jina', 'voyage', 'siliconflow'] as const;
export type RerankFormat = (typeof RERANK_FORMATS)[number];

export const RERANK_FORMAT_LABEL_KEYS: Record<RerankFormat, string> = {
  generic: 'common.nativeServices.formats.generic',
  cohere: 'common.nativeServices.formats.cohere',
  jina: 'common.nativeServices.formats.jina',
  voyage: 'common.nativeServices.formats.voyage',
  siliconflow: 'common.nativeServices.formats.siliconflow',
};
