/* eslint-disable */
import { MAX_HEROES } from '../game/rules.js';
import { factionRules } from '../game/factions.js';
import { leaderDefinitions } from '../cards/leaders.js';
import { models } from '../ai/models.js';
import { cardDescription, abilityNames } from './card.js';

export function showRulesGuide(arena) {
  arena.view.showDialog(
    '竞技规则 · 新手指南',
    '用有限的手牌争夺小局分数。卡牌不会互相普攻，战线分数的总和决定胜负。',
    [{ label: '明白了', action: () => arena.view.closeDialog() }],
  );
  const guide = document.createElement('div');
  guide.className = 'rules-guide';
  const section = (title, paragraphs, folded = false) => {
    const block = document.createElement(folded ? 'details' : 'section');
    const heading = document.createElement(folded ? 'summary' : 'h3');
    heading.textContent = title;
    block.append(heading);
    for (const text of paragraphs) {
      const p = document.createElement('p');
      p.textContent = text;
      block.append(p);
    }
    guide.append(block);
  };
  section('1. 赢的是小局，省的是整场手牌', [
    '每方两颗生命，通常以赢下两小局取胜。双方都放弃后，三条战线加起来分数更高的一方赢，败方失去一颗生命；生命归零即输掉整场。',
    '普通同分时双方各失去一颗生命；若双方同时归零，整场平局。只有一方是 Claude 时，该方赢同分小局。',
  ]);
  section('2. 开局与出牌', [
    '新手先选“均衡部署”。开局从卡组随机抽10张，最多换掉两张；调度中点击想换的牌，不换也可直接继续。',
    '双方轮流行动：点击一张手牌，再点击高亮战线部署；天气和部分技能牌点击后直接生效，需要选目标时按弹窗操作。一次行动通常出一张牌，或使用主动领袖。卡牌上的数字是战力，不是生命或攻击次数。',
    '推理前线、感知矩阵、算力集群是三条计分战线；牌按自身战线放置，双排牌可从两条指定战线中选一条。三线分数相加，不需要每条都赢。',
  ]);
  section('3. 放弃不是认输', [
    '“放弃本小局”表示本局不再行动，不能反悔；对手可以继续出牌，也可以放弃。双方都放弃才结算。若你已领先、对手已放弃，通常直接放弃就能保住手牌并赢下这一局。',
    '例：你30分，对手20分且已放弃，此时放弃即可赢；若你20分、对手30分且已放弃，还可继续追分，或接受失去一颗生命。若对手还未放弃，他仍能继续加分。',
    '“认输”会直接结束整场。即使本局落后，也可以放弃本局、保存资源争取后面的小局。',
  ]);
  section('4. 下一小局不会重新发10张', [
    '手牌跨小局保留，不洗回卡组、不自动补满。一般场上牌在结算后进入弃牌堆；未打出的手牌、剩余牌库和弃牌堆继续沿用。领袖的使用次数也不重置。',
    '补牌主要依靠侦察、部分技能/领袖与阵营被动；恢复从弃牌堆重新部署普通单位。只剩一张联结牌时，它不会和已经在弃牌堆或牌库里的牌联结。把全部输出用在首局，后面可能无牌可用。',
  ]);
  section('5. 领袖怎么选、怎么用', [
    '大厅点击“配置卡组”，在“领袖方案”下拉框从本阵营三个方案里选一个，保存后开战。预设已有领袖；更改并保存会生成自定义卡组。每套只有一个领袖，整场不能切换方案，领袖不在手牌或牌库中，也不计入四英雄上限。',
    '主动领袖整场限一次，占用一次行动。己方可行动时点击“领袖”按钮；悬停或点击旁边ⓘ查看当前效果，使用后仍可查看说明。DeepSeek“第三局重启”自动触发，不能主动点击；它替换原来的小局保留单位被动。',
  ]);
  section(
    '6. 常用技能与计分',
    [
      '普通单位会受天气、增益、联结和剪枝影响。英雄免疫天气、普通强化、普通恢复和剪枝；自身技能仍可触发。天气影响双方对应战线，不一定只伤对手。',
      '联结：同一战线、同名普通单位按在场数量倍增。两张基础4的联结牌，未受其他效果时各为8，总计16；不同名字或不同战线不联结。对齐增益给本战线其他普通单位+1；算力翻倍给本战线普通单位×2，同类翻倍不叠加，携带翻倍技能的单位不强化自身。',
      '天气先把普通单位基础战力压低到最多1，之后仍计算联结、对齐和翻倍。更完整的实际效果可悬停具体卡牌查看。',
      ...[
        'spy',
        'medic',
        'decoy',
        'scorch',
        'muster',
        'context_window',
        'safety_layer',
        'berserker',
        'avenger',
      ].map((id) => `${abilityNames[id]}：${ability_dict[id]?.description || '请查看卡牌说明。'}`),
    ],
    true,
  );
  section(
    '7. 四阵营被动',
    Object.entries(factionRules).map(
      ([id, rule]) => `${models[id].short} · ${rule.name}：${rule.description}`,
    ),
    true,
  );
  section(
    '8. 全部领袖方案',
    leaderDefinitions(arena.catalog).map((card) => `${card.name}\n${cardDescription(card)}`),
    true,
  );
  section(
    '9. 卡组、模式与限时',
    [
      `卡组至少22张单位（英雄也算单位），最多${MAX_HEROES}张英雄，最多10张特殊牌；每张卡还须遵守副本上限及阵营限制，中立牌可加入任意阵营。当前四套标准预设各26张，初学者可直接使用。`,
      '随机预设从本阵营四套标准配置中抽一套；随机组牌保留主题核心后替换部分单位。开战前可查看结果或重新随机，整场不会再随机换卡组。',
      '人机挑战与玩家对战由平台保存对局。普通行动30秒、开局换牌20秒、技能选目标15秒；按平台规则处理超时，连续三个普通回合超时判负。刷新或重连会恢复当前对局。AI观战仅在本机演示，不计账号成绩或奖励。',
    ],
    true,
  );
  document.getElementById('dialog-cards').append(guide);
}
