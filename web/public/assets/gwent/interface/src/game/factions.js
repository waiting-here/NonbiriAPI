export const factionRules = {
  openai: {
    name: '推演积累',
    description: '赢得一小局后，在下一小局开始时抽一张牌；牌库为空时不抽。',
  },
  deepseek: {
    name: '持续推理',
    description:
      '小局结束后随机保留己方一张普通单位；英雄和诱饵除外。保留单位的临时基础强化在新小局清除。',
  },
  claude: { name: '共识裁定', description: '小局战力相同时获胜；双方都是 Claude 时仍按平局处理。' },
  gemini: {
    name: '感知先机',
    description:
      '选择整场第一小局的先手。真人对战限时 15 秒，超时随机。双方都是 Gemini 时随机先手。',
  },
};
