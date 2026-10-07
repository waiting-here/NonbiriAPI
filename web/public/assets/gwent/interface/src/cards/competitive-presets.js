// Frozen standard presets; v0.28.2 updates only the four control counterkits.
// Provenance: docs/CONVERGENCE-ROUND2.md and earlier preset-search reports.
export const competitivePresets = {
  'openai/balanced': {
    leader: 'openai_leader',
    cards: [
      {
        id: 'openai_astra_hero',
        count: 1,
      },
      {
        id: 'openai_sol61_hero',
        count: 1,
      },
      {
        id: 'openai_sol6_hero',
        count: 1,
      },
      {
        id: 'openai_sol56_hero',
        count: 1,
      },
      {
        id: 'openai_infra_branch',
        count: 3,
      },
      {
        id: 'openai_infra_stream',
        count: 3,
      },
      {
        id: 'openai_infra_kv',
        count: 1,
      },
      {
        id: 'openai_infra_toolrouter',
        count: 1,
      },
      {
        id: 'openai_scout',
        count: 3,
      },
      {
        id: 'openai_restore',
        count: 2,
      },
      {
        id: 'openai_infra_cache',
        count: 1,
      },
      {
        id: 'openai_infra_memory',
        count: 1,
      },
      {
        id: 'openai_infra_judge',
        count: 1,
      },
      {
        id: 'openai_infra_sandbox',
        count: 1,
      },
      {
        id: 'openai_infra_planner',
        count: 1,
      },
      {
        id: 'clear_signal',
        count: 1,
      },
      {
        id: 'compute_surge',
        count: 1,
      },
      {
        id: 'safety_layer',
        count: 1,
      },
      {
        id: 'circuit_breaker',
        count: 1,
      },
    ],
    coreIds: ['openai_astra_hero', 'openai_infra_branch', 'openai_infra_stream'],
  },
  'openai/resource': {
    leader: 'openai_leader',
    cards: [
      {
        id: 'openai_astra_hero',
        count: 1,
      },
      {
        id: 'openai_sol61_hero',
        count: 1,
      },
      {
        id: 'openai_sol6_hero',
        count: 1,
      },
      {
        id: 'openai_sol56_hero',
        count: 1,
      },
      {
        id: 'openai_scout',
        count: 2,
      },
      {
        id: 'openai_infra_retrieval',
        count: 1,
      },
      {
        id: 'openai_restore',
        count: 2,
      },
      {
        id: 'openai_infra_memory',
        count: 1,
      },
      {
        id: 'openai_infra_cache',
        count: 1,
      },
      {
        id: 'openai_infra_branch',
        count: 3,
      },
      {
        id: 'openai_infra_stream',
        count: 3,
      },
      {
        id: 'openai_infra_sandbox',
        count: 1,
      },
      {
        id: 'openai_infra_judge',
        count: 1,
      },
      {
        id: 'openai_astra_reason',
        count: 3,
      },
      {
        id: 'clear_signal',
        count: 1,
      },
      {
        id: 'compute_surge',
        count: 1,
      },
      {
        id: 'safety_layer',
        count: 1,
      },
      {
        id: 'circuit_breaker',
        count: 1,
      },
    ],
    coreIds: [
      'openai_astra_hero',
      'openai_scout',
      'openai_infra_retrieval',
      'openai_restore',
      'openai_infra_branch',
      'openai_infra_stream',
    ],
  },
  'openai/bond': {
    leader: 'openai_leader_compute',
    cards: [
      {
        id: 'openai_astra_hero',
        count: 1,
      },
      {
        id: 'openai_sol61_hero',
        count: 1,
      },
      {
        id: 'openai_sol6_hero',
        count: 1,
      },
      {
        id: 'openai_sol56_hero',
        count: 1,
      },
      {
        id: 'openai_infra_embed',
        count: 3,
      },
      {
        id: 'openai_infra_stream',
        count: 3,
      },
      {
        id: 'openai_scout',
        count: 3,
      },
      {
        id: 'openai_infra_retrieval',
        count: 1,
      },
      {
        id: 'openai_infra_memory',
        count: 1,
      },
      {
        id: 'openai_infra_cache',
        count: 1,
      },
      {
        id: 'openai_restore',
        count: 2,
      },
      {
        id: 'openai_infra_visionbridge',
        count: 1,
      },
      {
        id: 'openai_infra_kv',
        count: 1,
      },
      {
        id: 'openai_infra_judge',
        count: 1,
      },
      {
        id: 'openai_infra_sandbox',
        count: 1,
      },
      {
        id: 'clear_signal',
        count: 1,
      },
      {
        id: 'compute_surge',
        count: 1,
      },
      {
        id: 'safety_layer',
        count: 1,
      },
      {
        id: 'circuit_breaker',
        count: 1,
      },
    ],
    coreIds: ['openai_astra_hero', 'openai_infra_embed', 'openai_infra_stream'],
  },
  'openai/control': {
    leader: 'openai_leader',
    cards: [
      {
        id: 'openai_astra_hero',
        count: 1,
      },
      {
        id: 'openai_sol61_hero',
        count: 1,
      },
      {
        id: 'openai_sol6_hero',
        count: 1,
      },
      {
        id: 'openai_sol56_hero',
        count: 1,
      },
      {
        id: 'openai_infra_branch',
        count: 3,
      },
      {
        id: 'openai_infra_beam',
        count: 2,
      },
      {
        id: 'openai_infra_judge',
        count: 1,
      },
      {
        id: 'openai_infra_sandbox',
        count: 1,
      },
      {
        id: 'openai_scout',
        count: 3,
      },
      {
        id: 'openai_restore',
        count: 2,
      },
      {
        id: 'openai_infra_cache',
        count: 1,
      },
      {
        id: 'openai_astra_reason',
        count: 2,
      },
      {
        id: 'openai_infra_toolrouter',
        count: 1,
      },
      {
        id: 'clear_signal',
        count: 1,
      },
      {
        id: 'circuit_breaker',
        count: 2,
      },
      {
        id: 'openai_infra_visionbridge',
        count: 1,
      },
      {
        id: 'openai_astra_tools',
        count: 1,
      },
      {
        id: 'cold_start',
        count: 1,
      },
    ],
    coreIds: [
      'openai_astra_hero',
      'openai_infra_branch',
      'openai_infra_judge',
      'openai_infra_sandbox',
    ],
  },
  'deepseek/balanced': {
    leader: 'deepseek_leader',
    cards: [
      {
        id: 'deepseek_v4pro_hero',
        count: 1,
      },
      {
        id: 'deepseek_v41flash_hero',
        count: 1,
      },
      {
        id: 'deepseek_v32exp_hero',
        count: 1,
      },
      {
        id: 'deepseek_v31terminus_hero',
        count: 1,
      },
      {
        id: 'deepseek_infra_branch',
        count: 3,
      },
      {
        id: 'deepseek_infra_embed',
        count: 3,
      },
      {
        id: 'deepseek_infra_visionbridge',
        count: 1,
      },
      {
        id: 'deepseek_infra_toolrouter',
        count: 1,
      },
      {
        id: 'deepseek_scout',
        count: 3,
      },
      {
        id: 'deepseek_restore',
        count: 2,
      },
      {
        id: 'deepseek_infra_cache',
        count: 1,
      },
      {
        id: 'deepseek_infra_memory',
        count: 1,
      },
      {
        id: 'deepseek_infra_judge',
        count: 1,
      },
      {
        id: 'deepseek_infra_sandbox',
        count: 1,
      },
      {
        id: 'deepseek_infra_planner',
        count: 1,
      },
      {
        id: 'clear_signal',
        count: 1,
      },
      {
        id: 'compute_surge',
        count: 1,
      },
      {
        id: 'safety_layer',
        count: 1,
      },
      {
        id: 'circuit_breaker',
        count: 1,
      },
    ],
    coreIds: ['deepseek_v4pro_hero', 'deepseek_infra_branch', 'deepseek_infra_embed'],
  },
  'deepseek/resource': {
    leader: 'deepseek_leader',
    cards: [
      {
        id: 'deepseek_v4pro_hero',
        count: 1,
      },
      {
        id: 'deepseek_v41flash_hero',
        count: 1,
      },
      {
        id: 'deepseek_v32exp_hero',
        count: 1,
      },
      {
        id: 'deepseek_v31terminus_hero',
        count: 1,
      },
      {
        id: 'deepseek_scout',
        count: 2,
      },
      {
        id: 'deepseek_infra_retrieval',
        count: 1,
      },
      {
        id: 'deepseek_restore',
        count: 3,
      },
      {
        id: 'deepseek_infra_memory',
        count: 2,
      },
      {
        id: 'deepseek_infra_branch',
        count: 3,
      },
      {
        id: 'deepseek_infra_stream',
        count: 3,
      },
      {
        id: 'deepseek_v4pro_reason',
        count: 3,
      },
      {
        id: 'clear_signal',
        count: 1,
      },
      {
        id: 'compute_surge',
        count: 1,
      },
      {
        id: 'safety_layer',
        count: 1,
      },
      {
        id: 'circuit_breaker',
        count: 1,
      },
      {
        id: 'retrieval_agent',
        count: 1,
      },
    ],
    coreIds: [
      'deepseek_v4pro_hero',
      'deepseek_scout',
      'deepseek_infra_retrieval',
      'deepseek_restore',
      'deepseek_infra_branch',
      'deepseek_infra_stream',
    ],
  },
  'deepseek/bond': {
    leader: 'deepseek_leader',
    cards: [
      {
        id: 'deepseek_v4pro_hero',
        count: 1,
      },
      {
        id: 'deepseek_v41flash_hero',
        count: 1,
      },
      {
        id: 'deepseek_v32exp_hero',
        count: 1,
      },
      {
        id: 'deepseek_v31terminus_hero',
        count: 1,
      },
      {
        id: 'deepseek_infra_embed',
        count: 3,
      },
      {
        id: 'deepseek_infra_stream',
        count: 3,
      },
      {
        id: 'deepseek_scout',
        count: 3,
      },
      {
        id: 'deepseek_infra_retrieval',
        count: 1,
      },
      {
        id: 'deepseek_infra_memory',
        count: 1,
      },
      {
        id: 'deepseek_infra_cache',
        count: 1,
      },
      {
        id: 'deepseek_restore',
        count: 2,
      },
      {
        id: 'deepseek_infra_visionbridge',
        count: 1,
      },
      {
        id: 'deepseek_infra_kv',
        count: 1,
      },
      {
        id: 'deepseek_infra_judge',
        count: 1,
      },
      {
        id: 'deepseek_infra_sandbox',
        count: 1,
      },
      {
        id: 'clear_signal',
        count: 1,
      },
      {
        id: 'compute_surge',
        count: 1,
      },
      {
        id: 'safety_layer',
        count: 1,
      },
      {
        id: 'circuit_breaker',
        count: 1,
      },
    ],
    coreIds: ['deepseek_v4pro_hero', 'deepseek_infra_embed', 'deepseek_infra_stream'],
  },
  'deepseek/control': {
    leader: 'deepseek_leader',
    cards: [
      {
        id: 'deepseek_v4pro_hero',
        count: 1,
      },
      {
        id: 'deepseek_v41flash_hero',
        count: 1,
      },
      {
        id: 'deepseek_v32exp_hero',
        count: 1,
      },
      {
        id: 'deepseek_v31terminus_hero',
        count: 1,
      },
      {
        id: 'deepseek_infra_branch',
        count: 3,
      },
      {
        id: 'deepseek_infra_beam',
        count: 1,
      },
      {
        id: 'deepseek_infra_judge',
        count: 2,
      },
      {
        id: 'deepseek_infra_sandbox',
        count: 2,
      },
      {
        id: 'deepseek_scout',
        count: 2,
      },
      {
        id: 'deepseek_restore',
        count: 1,
      },
      {
        id: 'deepseek_infra_cache',
        count: 1,
      },
      {
        id: 'deepseek_infra_memory',
        count: 1,
      },
      {
        id: 'deepseek_v4pro_reason',
        count: 2,
      },
      {
        id: 'deepseek_infra_toolrouter',
        count: 1,
      },
      {
        id: 'deepseek_infra_solver',
        count: 1,
      },
      {
        id: 'deepseek_infra_verifier',
        count: 1,
      },
      {
        id: 'clear_signal',
        count: 1,
      },
      {
        id: 'circuit_breaker',
        count: 2,
      },
      {
        id: 'cold_start',
        count: 1,
      },
    ],
    coreIds: [
      'deepseek_v4pro_hero',
      'deepseek_infra_branch',
      'deepseek_infra_judge',
      'deepseek_infra_sandbox',
    ],
  },
  'claude/balanced': {
    leader: 'claude_leader_retrieve',
    cards: [
      {
        id: 'claude_fable51_hero',
        count: 1,
      },
      {
        id: 'claude_opus55_hero',
        count: 1,
      },
      {
        id: 'claude_sonnet55_hero',
        count: 1,
      },
      {
        id: 'claude_opus5_hero',
        count: 1,
      },
      {
        id: 'claude_infra_branch',
        count: 3,
      },
      {
        id: 'claude_infra_embed',
        count: 3,
      },
      {
        id: 'claude_infra_visionbridge',
        count: 1,
      },
      {
        id: 'claude_infra_toolrouter',
        count: 1,
      },
      {
        id: 'claude_scout',
        count: 3,
      },
      {
        id: 'claude_restore',
        count: 2,
      },
      {
        id: 'claude_infra_cache',
        count: 1,
      },
      {
        id: 'claude_infra_memory',
        count: 1,
      },
      {
        id: 'claude_infra_judge',
        count: 1,
      },
      {
        id: 'claude_infra_sandbox',
        count: 1,
      },
      {
        id: 'claude_fable51_reason',
        count: 1,
      },
      {
        id: 'clear_signal',
        count: 1,
      },
      {
        id: 'compute_surge',
        count: 1,
      },
      {
        id: 'safety_layer',
        count: 1,
      },
      {
        id: 'circuit_breaker',
        count: 1,
      },
    ],
    coreIds: ['claude_fable51_hero', 'claude_infra_branch', 'claude_infra_embed'],
  },
  'claude/resource': {
    leader: 'claude_leader',
    cards: [
      {
        id: 'claude_fable51_hero',
        count: 1,
      },
      {
        id: 'claude_opus55_hero',
        count: 1,
      },
      {
        id: 'claude_sonnet55_hero',
        count: 1,
      },
      {
        id: 'claude_opus5_hero',
        count: 1,
      },
      {
        id: 'claude_infra_branch',
        count: 3,
      },
      {
        id: 'claude_infra_stream',
        count: 3,
      },
      {
        id: 'claude_infra_kv',
        count: 1,
      },
      {
        id: 'claude_infra_toolrouter',
        count: 1,
      },
      {
        id: 'claude_scout',
        count: 3,
      },
      {
        id: 'claude_infra_retrieval',
        count: 1,
      },
      {
        id: 'claude_restore',
        count: 2,
      },
      {
        id: 'claude_infra_cache',
        count: 1,
      },
      {
        id: 'claude_infra_memory',
        count: 1,
      },
      {
        id: 'claude_infra_sandbox',
        count: 1,
      },
      {
        id: 'claude_infra_judge',
        count: 1,
      },
      {
        id: 'clear_signal',
        count: 1,
      },
      {
        id: 'compute_surge',
        count: 1,
      },
      {
        id: 'safety_layer',
        count: 1,
      },
      {
        id: 'circuit_breaker',
        count: 1,
      },
    ],
    coreIds: ['claude_fable51_hero', 'claude_infra_branch', 'claude_infra_stream'],
  },
  'claude/bond': {
    leader: 'claude_leader',
    cards: [
      {
        id: 'claude_fable51_hero',
        count: 1,
      },
      {
        id: 'claude_opus55_hero',
        count: 1,
      },
      {
        id: 'claude_sonnet55_hero',
        count: 1,
      },
      {
        id: 'claude_opus5_hero',
        count: 1,
      },
      {
        id: 'claude_infra_embed',
        count: 3,
      },
      {
        id: 'claude_cluster',
        count: 3,
      },
      {
        id: 'claude_scout',
        count: 3,
      },
      {
        id: 'claude_infra_retrieval',
        count: 1,
      },
      {
        id: 'claude_infra_memory',
        count: 1,
      },
      {
        id: 'claude_infra_cache',
        count: 1,
      },
      {
        id: 'claude_restore',
        count: 2,
      },
      {
        id: 'claude_infra_visionbridge',
        count: 2,
      },
      {
        id: 'claude_infra_judge',
        count: 1,
      },
      {
        id: 'claude_infra_sandbox',
        count: 1,
      },
      {
        id: 'clear_signal',
        count: 1,
      },
      {
        id: 'compute_surge',
        count: 1,
      },
      {
        id: 'safety_layer',
        count: 1,
      },
      {
        id: 'circuit_breaker',
        count: 1,
      },
    ],
    coreIds: ['claude_fable51_hero', 'claude_infra_embed', 'claude_cluster'],
  },
  'claude/control': {
    leader: 'claude_leader_retrieve',
    cards: [
      {
        id: 'claude_fable51_hero',
        count: 1,
      },
      {
        id: 'claude_opus55_hero',
        count: 1,
      },
      {
        id: 'claude_sonnet55_hero',
        count: 1,
      },
      {
        id: 'claude_opus5_hero',
        count: 1,
      },
      {
        id: 'claude_infra_beam',
        count: 2,
      },
      {
        id: 'claude_infra_judge',
        count: 2,
      },
      {
        id: 'claude_infra_sandbox',
        count: 2,
      },
      {
        id: 'claude_scout',
        count: 3,
      },
      {
        id: 'claude_restore',
        count: 2,
      },
      {
        id: 'claude_infra_cache',
        count: 1,
      },
      {
        id: 'claude_infra_memory',
        count: 1,
      },
      {
        id: 'claude_fable51_reason',
        count: 2,
      },
      {
        id: 'claude_infra_solver',
        count: 2,
      },
      {
        id: 'claude_vision',
        count: 1,
      },
      {
        id: 'clear_signal',
        count: 1,
      },
      {
        id: 'circuit_breaker',
        count: 2,
      },
      {
        id: 'cold_start',
        count: 1,
      },
    ],
    coreIds: [
      'claude_fable51_hero',
      'claude_infra_beam',
      'claude_infra_judge',
      'claude_infra_sandbox',
    ],
  },
  'gemini/balanced': {
    leader: 'gemini_leader_clear',
    cards: [
      {
        id: 'gemini_flash38_hero',
        count: 1,
      },
      {
        id: 'gemini_pro31_hero',
        count: 1,
      },
      {
        id: 'gemini_live38_hero',
        count: 1,
      },
      {
        id: 'gemini_flash37_hero',
        count: 1,
      },
      {
        id: 'gemini_infra_branch',
        count: 3,
      },
      {
        id: 'gemini_infra_embed',
        count: 3,
      },
      {
        id: 'gemini_infra_visionbridge',
        count: 1,
      },
      {
        id: 'gemini_infra_toolrouter',
        count: 1,
      },
      {
        id: 'gemini_scout',
        count: 3,
      },
      {
        id: 'gemini_restore',
        count: 2,
      },
      {
        id: 'gemini_infra_cache',
        count: 1,
      },
      {
        id: 'gemini_infra_memory',
        count: 1,
      },
      {
        id: 'gemini_infra_judge',
        count: 1,
      },
      {
        id: 'gemini_infra_sandbox',
        count: 1,
      },
      {
        id: 'gemini_infra_planner',
        count: 1,
      },
      {
        id: 'clear_signal',
        count: 1,
      },
      {
        id: 'compute_surge',
        count: 1,
      },
      {
        id: 'safety_layer',
        count: 1,
      },
      {
        id: 'circuit_breaker',
        count: 1,
      },
    ],
    coreIds: ['gemini_flash38_hero', 'gemini_infra_branch', 'gemini_infra_embed'],
  },
  'gemini/resource': {
    leader: 'gemini_leader_clear',
    cards: [
      {
        id: 'gemini_flash38_hero',
        count: 1,
      },
      {
        id: 'gemini_pro31_hero',
        count: 1,
      },
      {
        id: 'gemini_live38_hero',
        count: 1,
      },
      {
        id: 'gemini_flash37_hero',
        count: 1,
      },
      {
        id: 'gemini_scout',
        count: 2,
      },
      {
        id: 'gemini_infra_retrieval',
        count: 2,
      },
      {
        id: 'gemini_restore',
        count: 2,
      },
      {
        id: 'gemini_infra_memory',
        count: 1,
      },
      {
        id: 'gemini_infra_cache',
        count: 1,
      },
      {
        id: 'gemini_infra_branch',
        count: 3,
      },
      {
        id: 'gemini_infra_stream',
        count: 3,
      },
      {
        id: 'gemini_infra_judge',
        count: 1,
      },
      {
        id: 'gemini_flash38_reason',
        count: 2,
      },
      {
        id: 'clear_signal',
        count: 1,
      },
      {
        id: 'compute_surge',
        count: 1,
      },
      {
        id: 'safety_layer',
        count: 1,
      },
      {
        id: 'circuit_breaker',
        count: 1,
      },
      {
        id: 'gemini_infra_rollback',
        count: 1,
      },
    ],
    coreIds: [
      'gemini_flash38_hero',
      'gemini_scout',
      'gemini_infra_retrieval',
      'gemini_restore',
      'gemini_infra_branch',
      'gemini_infra_stream',
    ],
  },
  'gemini/bond': {
    leader: 'gemini_leader_clear',
    cards: [
      {
        id: 'gemini_flash38_hero',
        count: 1,
      },
      {
        id: 'gemini_pro31_hero',
        count: 1,
      },
      {
        id: 'gemini_live38_hero',
        count: 1,
      },
      {
        id: 'gemini_flash37_hero',
        count: 1,
      },
      {
        id: 'gemini_infra_embed',
        count: 3,
      },
      {
        id: 'gemini_infra_stream',
        count: 3,
      },
      {
        id: 'gemini_scout',
        count: 3,
      },
      {
        id: 'gemini_infra_retrieval',
        count: 1,
      },
      {
        id: 'gemini_infra_memory',
        count: 1,
      },
      {
        id: 'gemini_infra_cache',
        count: 1,
      },
      {
        id: 'gemini_restore',
        count: 2,
      },
      {
        id: 'gemini_infra_visionbridge',
        count: 1,
      },
      {
        id: 'gemini_infra_kv',
        count: 1,
      },
      {
        id: 'gemini_infra_judge',
        count: 1,
      },
      {
        id: 'gemini_infra_sandbox',
        count: 1,
      },
      {
        id: 'clear_signal',
        count: 1,
      },
      {
        id: 'compute_surge',
        count: 1,
      },
      {
        id: 'safety_layer',
        count: 1,
      },
      {
        id: 'circuit_breaker',
        count: 1,
      },
    ],
    coreIds: ['gemini_flash38_hero', 'gemini_infra_embed', 'gemini_infra_stream'],
  },
  'gemini/control': {
    leader: 'gemini_leader_clear',
    cards: [
      {
        id: 'gemini_flash38_hero',
        count: 1,
      },
      {
        id: 'gemini_pro31_hero',
        count: 1,
      },
      {
        id: 'gemini_live38_hero',
        count: 1,
      },
      {
        id: 'gemini_flash37_hero',
        count: 1,
      },
      {
        id: 'gemini_infra_beam',
        count: 2,
      },
      {
        id: 'gemini_infra_judge',
        count: 2,
      },
      {
        id: 'gemini_infra_sandbox',
        count: 2,
      },
      {
        id: 'gemini_scout',
        count: 3,
      },
      {
        id: 'gemini_restore',
        count: 2,
      },
      {
        id: 'gemini_infra_cache',
        count: 1,
      },
      {
        id: 'gemini_infra_memory',
        count: 1,
      },
      {
        id: 'gemini_flash38_reason',
        count: 2,
      },
      {
        id: 'gemini_infra_solver',
        count: 2,
      },
      {
        id: 'gemini_vision',
        count: 1,
      },
      {
        id: 'clear_signal',
        count: 1,
      },
      {
        id: 'circuit_breaker',
        count: 2,
      },
      {
        id: 'cold_start',
        count: 1,
      },
    ],
    coreIds: [
      'gemini_flash38_hero',
      'gemini_infra_beam',
      'gemini_infra_judge',
      'gemini_infra_sandbox',
    ],
  },
};
