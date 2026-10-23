package requestkind

// CharityKindsSQL is the closed SQL IN list used by admission and privacy
// projections. It contains only protocol identities defined by this package.
const CharityKindsSQL = "('charity_chat_completions','charity_embeddings','charity_images_generations','charity_decisions','charity_responses','charity_responses_compact','charity_responses_input_tokens','charity_completions','charity_images_edits','charity_images_variations','charity_audio_speech','charity_audio_transcriptions','charity_audio_translations','charity_moderations','charity_rerank','charity_systemone')"
