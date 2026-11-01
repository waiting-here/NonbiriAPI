package db

// CharityLogCoveringIndexSQL provides the page sort key without reading wide
// request rows. Only the selected page loads display columns from request_logs.
const CharityLogCoveringIndexSQL = `CREATE INDEX idx_request_logs_charity_model ON request_logs(route_kind,model,completed_at,id,started_at) WHERE route_kind IN ('charity_chat_completions','charity_embeddings','charity_images_generations','charity_decisions','charity_responses','charity_responses_compact','charity_responses_input_tokens','charity_completions','charity_images_edits','charity_images_variations','charity_audio_speech','charity_audio_transcriptions','charity_audio_translations','charity_moderations','charity_rerank','charity_systemone');
`
