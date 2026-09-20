import type { Translate } from '../labels';
import { tutorialData } from './data';

export interface TutorialStep {
  kind: 'intro' | 'loadout' | 'matching' | 'plan' | 'resolution' | 'review' | 'finish';
  target: string;
  value?: string;
  round: number;
  title: string;
  body: string;
}
export function tutorialSteps(t: Translate): TutorialStep[] {
  const steps: TutorialStep[] = [];
  const add = (
    kind: TutorialStep['kind'],
    target: string,
    title: string,
    body: string,
    round = 0,
    value?: string,
  ) => steps.push({ kind, target, title, body, round, value });
  add(
    'intro',
    'continue',
    t('一起打一场练习赛', 'Let’s play a practice match'),
    t(
      '目标是先达到 60 赞。双方同时选招，共用电池，各自管理 Token 和金币。这次由教学机器人陪你完成一局，所有操作都只用于练习，不会匹配真实玩家、扣积分或发奖励。可以随时跳过。',
      'Race to 60 likes. Both players choose together, share a battery and manage their own tokens and gold. A teaching bot will play a complete match with you. This practice never matches real players, spends credits or awards prizes. You can skip at any time.',
    ),
  );
  add(
    'loadout',
    'role:ChatGPT',
    t('第一步：选择 ChatGPT', 'First: choose ChatGPT'),
    t(
      '点击高亮角色。她的固有被动「最强多模态」提供图像额度，也能使用订阅和 API。这次会体验净化、重置和倍速。',
      'Select the highlighted character. Her Multimodal passive provides image quota alongside subscriptions and API. We’ll try cleansing, resets and speed mode.',
    ),
  );
  add(
    'loadout',
    'harness:H01',
    t('带上 Codex', 'Equip Codex'),
    t(
      'Harness 决定技能槽和固定被动。Codex 提供第五个技能槽，并为符合条件的技能增加基础点赞。',
      'A harness determines skill slots and fixed passives. Codex adds a fifth slot and increases base likes for qualifying skills.',
    ),
  );
  const equipment = [
    [
      'PUB42',
      '用量重置',
      'Usage reset',
      '双方订阅和图像额度回满，留到用量低时再用。',
      'Refills both subscriptions and images. Save it until quotas are low.',
    ],
    [
      'GPT01',
      'Hello, world!',
      'Hello, world!',
      '稳定普攻，连续使用可叠缓存，让后续普攻更省 Token。',
      'A stable basic attack. Repeated casts build cache that saves tokens.',
    ],
    [
      'GPT41',
      '重新生成',
      'Regenerate',
      '花费 API 余量，净化自己的负面效果。',
      'Spend API reserve to cleanse your debuffs.',
    ],
    [
      'GPT44',
      '加速猛蹬',
      'Pedal faster',
      '下轮开启倍速，得赞翻倍，但基础 Token 和耗电也增至三倍。',
      'Enable speed next round: twice the likes, but triple base tokens and energy.',
    ],
    [
      'GPT61',
      '图文并茂',
      'Words and pictures',
      '消耗图像额度的大招，获得大量点赞与后续加赞效果。',
      'An image-consuming ultimate that earns many likes and buffs later casts.',
    ],
  ];
  for (const [id, zh, en, bodyZh, bodyEn] of equipment)
    add('loadout', `equip:${id}`, t(`装入「${zh}」`, `Equip ${en}`), t(bodyZh, bodyEn));
  add(
    'loadout',
    'continue',
    t('配装完成', 'Loadout ready'),
    t(
      '五个槽位已装好。卡片直接显示效果和基础费用；正式对局中可打开详情阅读关联词条。接下来演示匹配。',
      'All five slots are ready. Cards show effects and base costs; in live games you can open details and follow linked terms. Next we’ll demonstrate matching.',
    ),
  );
  add(
    'matching',
    'continue',
    t('匹配演示：找到教学机器人', 'Match demo: teaching bot found'),
    t(
      '正式匹配会预留入场积分，最多等待 120 秒，成功前可以取消并退款。这里没有真实排队或付款。对手是未携带 Harness 的 Claude；她领先时，固有被动会为原本得赞的主技能增加 1 点基础得赞。点击继续入场。',
      'Live matching reserves your entry credits and waits up to 120 seconds; cancel before matching for a refund. No queue or payment exists here. Your opponent is Claude without a harness. When ahead, her character passive adds one base like to a main skill with positive original likes. Continue to enter.',
    ),
  );
  const lessons = [
    [
      '先看资源，再选招',
      'Read resources before choosing',
      '双方共享上方电池，订阅瞬发与总量在同一个方框内，API 是独立余额。正式对局每轮只有 20 秒；未确认就会跳过，不足 5 秒时有视觉提醒，开启音效后也会响铃。教学讲解会等你。这轮先用 API 支付，保留订阅。',
      'The battery above is shared. Subscription burst and total share a panel; API is a separate balance. Live turns last 20 seconds: an unconfirmed plan skips casting. Below five seconds the UI warns you, with sound when enabled. This tutorial waits for you. Pay with API this round to preserve subscription quota.',
    ],
    [
      '净化，追回得赞',
      'Cleanse to protect your likes',
      '对手施加了温馨约束，会降低本轮得赞。重新生成先净化，再计算得赞；它使用 API。选择技能后，还要选中要移除的状态。',
      'The opponent applied a debuff that reduces your likes this round. Regenerate uses API and cleanses before scoring. After choosing the skill, select the status to remove.',
    ],
    [
      '先购物，再出招',
      'Shop before casting',
      '升级订阅会同时增加瞬发、总量和图像额度，消耗游戏内金币。这些金币属于这场对局，不是站点积分。对手开始积蓄优势，我们先扩充资源。',
      'Upgrading adds burst, total and image quota using this match’s gold, not your site credits. The opponent is gaining ground; build your resources first.',
    ],
    [
      '电池快空了',
      'The battery is running low',
      '对手的大招把我们拉开了差距，也耗去了大量电能。先充电，再净化宪法约束。充电补充双方共用的电池，不能只看自己的技能费用。',
      'The opponent’s ultimate built a large lead and drained the battery. Charge first, then cleanse the new debuff. Charging fills the shared battery, so consider both players’ costs.',
    ],
    [
      '大招反击',
      'Counter with an ultimate',
      '现在图像和订阅额度足够，使用图文并茂追分。Codex 的基础加成与图像加成一起生效，之后还能获得两轮加赞效果。',
      'You have enough images and subscription quota. Use Words and pictures to catch up. Codex adds its base and image bonuses, and the skill buffs likes for two following rounds.',
    ],
    [
      '重置有代价，也有回报',
      'A reset has tradeoffs',
      '我们的用量已经明显下降。用量重置消耗金币与电能，恢复双方订阅和图像，不能挽救同轮已失败的付款。挑选时机，借助剩余的加赞效果争取小幅领先。',
      'Your quotas have fallen. Usage reset costs gold and energy and restores both players’ subscriptions and images, without rescuing failed payments this round. Time it with your remaining likes buff to gain a narrow lead.',
    ],
    [
      '暂时落后，换取爆发',
      'Fall behind now to accelerate',
      '开启倍速这轮不拿赞，对手会再次领先。模式从下轮生效：得赞 ×2，基础 Token 和电能 ×3。确认前先看之后是否付得起。',
      'Toggling speed scores no likes this round, so the opponent will lead again. From next round likes are doubled, while base tokens and energy triple. Check that you can afford the next casts.',
    ],
    [
      '倍速后的第一次普攻',
      'Your first accelerated attack',
      '使用 Hello, world!。观察倍速与 Codex 共同带来的得赞，同时留意加快的资源消耗。',
      'Use Hello, world! and watch speed combine with Codex for more likes, alongside faster resource consumption.',
    ],
    [
      '缓存帮助续航',
      'Cache keeps you going',
      '继续普攻。上一轮获得的缓存开始减免 Token；共享电池仍在减少，最后几轮要算好余量。',
      'Continue with the basic attack. Last round’s cache now reduces token costs. Shared energy is still falling, so account for the remaining rounds.',
    ],
    [
      '最后一轮，抓住机会',
      'One last push',
      '我们已经小幅领先。资源仍足够支撑这次普攻，确认方案，争取守住优势。',
      'You have a narrow lead and enough resources for this basic attack. Lock in your plan for the final push.',
    ],
  ];
  for (let i = 0; i < tutorialData.rounds.length; i++) {
    const round = i + 1,
      plan = tutorialData.rounds[i].plan;
    add(
      'plan',
      'continue',
      t(lessons[i][0], lessons[i][1]),
      t(lessons[i][2], lessons[i][3]),
      round,
    );
    if (plan.purchases.length) {
      add(
        'plan',
        'tab:shop',
        t('打开购物', 'Open the shop'),
        t('点击高亮的购物标签。', 'Select the highlighted Shop tab.'),
        round,
      );
      const item = plan.purchases[0].item;
      add(
        'plan',
        `buy:${item}`,
        item === 'sub'
          ? t('升级订阅', 'Upgrade subscription')
          : t('为共享电池充电', 'Charge the shared battery'),
        t(
          '把高亮商品加入本轮方案。确认方案时才会和技能一起结算。',
          'Add the highlighted purchase. It resolves with your skills when the plan is confirmed.',
        ),
        round,
      );
      add(
        'plan',
        'tab:skills',
        t('回到技能', 'Return to skills'),
        t(
          '购物已加入方案，继续选择本轮技能。',
          'The purchase is in your plan. Now choose your skill.',
        ),
        round,
      );
    }
    add(
      'plan',
      `cast:${plan.main!.skillId}`,
      t('选择高亮技能', 'Choose the highlighted skill'),
      t(
        '技能选择后仍需确认方案。正式对局中，确认之前可以调整选择。',
        'Choosing a skill does not lock your plan. In a live match you can adjust choices before confirming.',
      ),
      round,
    );
    if (plan.main!.pay === 'api')
      add(
        'plan',
        'payment',
        t('本轮使用 API', 'Use API this round'),
        t('把付款方式改为「使用 API 余量」。', 'Set payment to “Use API reserve”.'),
        round,
        'api',
      );
    if (plan.main!.targets?.length)
      add(
        'plan',
        `target:${plan.main!.targets[0]}`,
        t('选中要净化的效果', 'Choose the debuff to cleanse'),
        t(
          '勾选高亮的负面效果，确认要移除的对象。',
          'Check the highlighted debuff to choose what to remove.',
        ),
        round,
      );
    add(
      'plan',
      'lock',
      t('检查方案并确认', 'Review and lock your plan'),
      t(
        '确认后不可更改。教学机器人会提交固定方案，双方同时揭示并逐步结算。',
        'You cannot change a locked plan. The teaching bot submits its scripted choice; both plans are revealed and resolved together.',
      ),
      round,
    );
    add(
      'resolution',
      'resolution',
      t('观看本轮结算', 'Watch the round resolve'),
      t(
        '购物 → 付款 → 净化 → 得赞 → 后续变化。观察资源和状态出现的时机。',
        'Shopping → payment → cleansing → likes → later changes. Watch when resources and statuses change.',
      ),
      round,
    );
    const scores = tutorialData.rounds[i].after.players.map((p) => p.likes);
    add(
      i === 9 ? 'finish' : 'review',
      'continue',
      i === 9
        ? t(
            `${scores[0]}∶${scores[1]}，艰难取胜！`,
            `${scores[0]}–${scores[1]}: a hard-fought victory!`,
          )
        : t(`本轮结束：${scores[0]}∶${scores[1]}`, `Round complete: ${scores[0]}–${scores[1]}`),
      i === 9
        ? t(
            '你经历了落后、净化、资源补充和反击，最后靠倍速与缓存险胜。这只是教学剧本；正式对局的对手会自主选择。可把教学配装填入大厅，再自行决定是否匹配。',
            'You fell behind, cleansed, replenished resources and fought back, finally winning narrowly with speed and cache. This was a teaching script; live opponents make their own choices. You may copy the teaching loadout to the lobby and decide when to match.',
          )
        : t(
            '看完变化后再继续。正式对局在结算演出结束后开始新的完整 20 秒。',
            'Continue when you have reviewed the changes. Live games start a fresh full 20 seconds after the presentation ends.',
          ),
      round,
    );
  }
  return steps;
}
