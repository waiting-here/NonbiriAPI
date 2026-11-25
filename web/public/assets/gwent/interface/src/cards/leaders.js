// Alternate leader configurations reuse the four existing illustrations and base catalog entries.
const options = {
  openai: [
    ['assault', '前线统筹', 'leader_openai_assault'],
    ['compute', '算力统筹', 'leader_openai_compute'],
  ],
  deepseek: [
    ['recover', '检查点调度', 'leader_deepseek_recover'],
    ['rebirth', '第三局重启', 'leader_deepseek_rebirth'],
  ],
  claude: [
    ['deny', '协议封锁', 'leader_claude_deny'],
    ['retrieve', '上下文回收', 'leader_claude_retrieve'],
  ],
  gemini: [
    ['clear', '晴空解析', 'leader_gemini_clear'],
    ['sensors', '感知统筹', 'leader_gemini_sensors'],
  ],
};

export function leaderDefinitions(catalog) {
  return catalog
    .filter((card) => card.type === 'leader')
    .flatMap((source) => [
      source,
      ...(options[source.faction] || []).map(([suffix, title, ability]) => ({
        ...source,
        id: `${source.faction}_leader_${suffix}`,
        name: `${source.name} · ${title}`,
        abilities: [ability],
        choiceOf: source.id,
      })),
    ]);
}
