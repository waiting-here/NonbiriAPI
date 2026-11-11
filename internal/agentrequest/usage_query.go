package agentrequest

// Complete hours use the transactionally maintained rollup. Only the two
// partial hours read individual facts, preserving the open/closed time bounds.
const usageWindowQuery = `WITH buckets AS (
 SELECT source,kind,actor_id,requests,input_tokens,cached_input_tokens,cache_write_input_tokens,output_tokens,reasoning_tokens,unknown_count
 FROM agent_request_usage_hours WHERE hour>=? AND hour<?
 UNION ALL
 SELECT source,kind,actor_id,1,input_tokens,cached_input_tokens,cache_write_input_tokens,output_tokens,reasoning_tokens,unknown
 FROM agent_request_usage WHERE recorded_at>? AND recorded_at<?
 UNION ALL
 SELECT source,kind,actor_id,1,input_tokens,cached_input_tokens,cache_write_input_tokens,output_tokens,reasoning_tokens,unknown
 FROM agent_request_usage WHERE recorded_at>=? AND recorded_at<=?
), totals AS MATERIALIZED (
 SELECT source,kind,actor_id,SUM(requests) requests,SUM(input_tokens) input_tokens,
 SUM(cached_input_tokens) cached_input_tokens,SUM(cache_write_input_tokens) cache_write_input_tokens,
 SUM(output_tokens) output_tokens,SUM(reasoning_tokens) reasoning_tokens,SUM(unknown_count) unknown_count
 FROM buckets GROUP BY source,kind,actor_id
)
SELECT u.source,u.kind,u.actor_id,t.actor_role,t.actor_label,u.requests,u.input_tokens,u.cached_input_tokens,
u.cache_write_input_tokens,u.output_tokens,u.reasoning_tokens,u.unknown_count
FROM totals u JOIN agent_request_usage_totals t ON t.source=u.source AND t.kind=u.kind AND t.actor_id=u.actor_id
ORDER BY u.source,u.kind,u.actor_id`
