import { DIALOGUES, formatWhaleLine, displayNeighborName } from './catalog';
import type { GameSnapshot, GuestWhaleVisit, WhaleExpression, WhaleOutfitId } from './types';
export function getActiveGuests(
  snapshot: Pick<GameSnapshot, 'guestVisit' | 'guestVisits'>,
): GuestWhaleVisit[] {
  if (Array.isArray(snapshot.guestVisits)) {
    return snapshot.guestVisits;
  }
  if (snapshot.guestVisit) {
    return [{ ...snapshot.guestVisit, slotIndex: snapshot.guestVisit.slotIndex ?? 0 }];
  }
  return [];
}

export function resolveWhaleExpression(
  snapshot: GameSnapshot,
  now: number,
  values: Record<string, number>,
  workingAway: boolean,
): { expression: WhaleExpression; statusLabel: string; speech: string } {
  const { whale, home } = snapshot;

  if (whale.location.kind === 'visiting') {
    const visitingId = whale.location.neighborId;
    const host = snapshot.neighbors.find((n) => n.id === visitingId);
    const hostName = host ? displayNeighborName(host) : '邻家小筑';
    return {
      expression: 'cheerful',
      statusLabel: workingAway ? '在外帮工' : '外出串门',
      speech: workingAway
        ? `我在【${hostName}】家帮工呢，收工就回小筑～`
        : `阿姆阿姆……【${hostName}】家的白米饭真香！`,
    };
  }

  if (whale.tempEmotion && whale.tempEmotion.expiresAt > now) {
    const labelMap: Record<WhaleExpression, string> = {
      cheerful: '元气愉悦',
      calm: '发呆放空中',
      pout: '鼓腮抗议中',
      panicked: '惊慌冒汗',
      teary: '委屈巴巴',
    };
    return {
      expression: whale.tempEmotion.expression,
      statusLabel: labelMap[whale.tempEmotion.expression],
      speech: whale.tempEmotion.line,
    };
  }

  if (whale.isSleeping) {
    // 睡觉时每 60 秒才偶尔冒出 10 秒梦话，其余 50 秒安静挂着 Zzz 泡泡呼吸
    const dreamCycleMs = 60_000;
    const dreamShowMs = 10_000;
    const inDreamWindow = now % dreamCycleMs < dreamShowMs;
    const dreamIdx = Math.floor(now / dreamCycleMs) % DIALOGUES.sleepingDreams.length;
    const sleptMin = whale.sleepingSince ? Math.floor((now - whale.sleepingSince) / 60_000) : 0;
    const sleepReady = sleptMin >= Math.round(values.sleep_qualify_seconds / 60);
    return {
      expression: 'calm',
      statusLabel: sleepReady ? '已睡饱，唤醒可获得午睡元气' : `趴窝呼呼大睡 (${sleptMin} 分钟)`,
      speech: inDreamWindow
        ? formatWhaleLine(DIALOGUES.sleepingDreams[dreamIdx], whale.name, whale.outfit)
        : '',
    };
  }

  const outfitId = whale.outfit ?? 'maid_classic';
  const outfitSpecials = DIALOGUES.outfitIdleSpecials[outfitId] ?? [];

  // 日常待机每 52 秒才开口自言自语 11 秒，其余 41 秒安安静静游动/干饭不刷屏；严重饥饿时稍缩短间隔提醒投喂
  const idleCycleMs = whale.satiety < 30 && home.troughGrams <= 0 ? 36_000 : 52_000;
  const idleShowMs = 11_000;
  const isSpeakingNow = now % idleCycleMs < idleShowMs;
  const slot = Math.floor(now / idleCycleMs);

  const happyPool =
    outfitSpecials.length > 0 ? [...outfitSpecials, ...DIALOGUES.idleHappy] : DIALOGUES.idleHappy;

  if (home.troughGrams > 0) {
    const fullPool = DIALOGUES.idleFull;
    return {
      expression: 'cheerful',
      statusLabel: `捧碗炫饭 · 叮当攒币中`,
      speech: !isSpeakingNow
        ? ''
        : whale.satiety >= 90
          ? formatWhaleLine(fullPool[slot % fullPool.length], whale.name, outfitId)
          : formatWhaleLine(happyPool[slot % happyPool.length], whale.name, outfitId),
    };
  }

  if (whale.satiety < 30) {
    return {
      expression: 'teary',
      statusLabel: '饭盆见底，想吃白米饭',
      speech: isSpeakingNow
        ? formatWhaleLine(
            DIALOGUES.idleStarving[slot % DIALOGUES.idleStarving.length],
            whale.name,
            outfitId,
          )
        : '',
    };
  }

  if (whale.satiety < 60) {
    return {
      expression: 'pout',
      statusLabel: '饭盆见底 · 嘴硬催饭中',
      speech: isSpeakingNow
        ? formatWhaleLine(
            DIALOGUES.idleSlightlyHungry[slot % DIALOGUES.idleSlightlyHungry.length],
            whale.name,
            outfitId,
          )
        : '',
    };
  }

  if (whale.satiety < 78) {
    return {
      expression: 'calm',
      statusLabel: '服务器繁忙 · 死鱼眼摸鱼中',
      speech: isSpeakingNow
        ? formatWhaleLine(
            DIALOGUES.idleDeadpan[slot % DIALOGUES.idleDeadpan.length],
            whale.name,
            outfitId,
          )
        : '',
    };
  }

  return {
    expression: 'cheerful',
    statusLabel: '摸鱼待机中',
    speech: isSpeakingNow
      ? formatWhaleLine(happyPool[slot % happyPool.length], whale.name, outfitId)
      : '',
  };
}

function formatMultiWhaleLine(
  template: string,
  vars: {
    host: string;
    hostOutfit?: WhaleOutfitId;
    guest?: string;
    guestA?: string;
    guestB?: string;
  },
): string {
  const withNames = template
    .replace(/\{host\}/g, vars.host)
    .replace(/\{name\}/g, vars.host)
    .replace(/\{guest\}/g, vars.guest ?? '小鲸')
    .replace(/\{guestA\}/g, vars.guestA ?? '小鲸A')
    .replace(/\{guestB\}/g, vars.guestB ?? '小鲸B');
  return formatWhaleLine(withNames, vars.host, vars.hostOutfit);
}

export interface StageConversationState {
  /** 自家鲸鱼娘当前应显示的台词（可能来自单鲸状态、点播唠嗑、双鲸对话或三鲸聚会） */
  readonly hostSpeech: string;
  /** 每只客鲸（neighborId -> 气泡台词）的实时台词映射 */
  readonly guestSpeeches: Record<string, string>;
  /** 当前是否处于多鲸同屏一唱一和剧场模式 */
  readonly banterMode: 'solo' | 'duo_sneak' | 'duo_work' | 'trio_party';
}

/**
 * 协调舞台上所有鲸鱼娘的气泡台词（低频克制呼吸感设计）：
 * - 平时大部分时间安安静静干饭/散步，不挂常驻气泡遮挡画面
 * - 客鲸刚到场前 8 秒、抓包退场时，或每隔约 56 秒的短窗口才冒出一句潜行/打工独白
 * - 双鲸/三鲸同堂时，约每 68 秒才自然上演 12 秒的一唱一和群像小剧场（玩家主动点「唠嗑/三鲸群聊」或点击气泡时则立即展示 12 秒！）
 */
export function resolveStageConversations(
  snapshot: GameSnapshot,
  baseHostSpeech: string,
  now = Date.now(),
  banterSeed = 0,
  manualBanterUntil = 0,
): StageConversationState {
  const { whale, home } = snapshot;
  const allGuests = getActiveGuests(snapshot);
  const guestSpeeches: Record<string, string> = {};

  if (allGuests.length === 0) {
    return { hostSpeech: baseHostSpeech, guestSpeeches, banterMode: 'solo' };
  }

  const isManualBanterActive =
    banterSeed > 0 && (manualBanterUntil === 0 || now < manualBanterUntil);
  const banterCycleMs = 68_000;
  const banterShowMs = 12_000;
  const autoBanterWindow = now % banterCycleMs < banterShowMs;
  const slotTick = Math.floor(now / banterCycleMs) + banterSeed;
  const hasManualTemp = Boolean(whale.tempEmotion && whale.tempEmotion.expiresAt > now);

  // 1. 客鲸独立低频台词：退场演出必现；刚进门/刚上岗前 8 秒冒一句；其余每 56 秒错峰冒出 9 秒
  const guestSoloCycleMs = 56_000;
  const guestSoloShowMs = 9_500;
  allGuests.forEach((g, idx) => {
    if (g.stage !== 'eating' && g.stage !== 'working') {
      guestSpeeches[g.neighborId] = g.bubbleLine ?? '呜哇～先溜一步咯！';
      return;
    }
    const justChangedStage =
      now - g.arrivedAt < 8_000 || (g.hiredAt !== undefined && now - g.hiredAt < 8_000);
    const staggerMs = idx * 23_000 + (g.neighborId.charCodeAt(g.neighborId.length - 1) % 7) * 3_500;
    const inSoloWindow = (now + staggerMs) % guestSoloCycleMs < guestSoloShowMs;

    if (!justChangedStage && !inSoloWindow) {
      guestSpeeches[g.neighborId] = '';
      return;
    }

    const soloTick =
      Math.floor((now + staggerMs) / guestSoloCycleMs) +
      idx * 5 +
      g.neighborId.charCodeAt(g.neighborId.length - 1);
    if (g.stage === 'working') {
      if (home.troughGrams <= 0) {
        const emptyPool = DIALOGUES.guestWorkEmptyLines;
        guestSpeeches[g.neighborId] = emptyPool[soloTick % emptyPool.length];
      } else {
        const workPool = DIALOGUES.guestWorkLines;
        guestSpeeches[g.neighborId] = workPool[soloTick % workPool.length];
      }
    } else {
      const sneakPool = DIALOGUES.guestSneakLines;
      const guestMeta = snapshot.neighbors.find((n) => n.id === g.neighborId);
      guestSpeeches[g.neighborId] = formatWhaleLine(
        sneakPool[soloTick % sneakPool.length],
        guestMeta?.whaleName,
        guestMeta?.whaleOutfit,
      );
    }
  });

  // 仅统计当前正在偷吃或打工的在场客鲸，用于触发双鲸/三鲸联动对话
  const interactiveGuests = [...allGuests]
    .filter((g) => g.stage === 'eating' || g.stage === 'working')
    .sort((a, b) => (a.slotIndex ?? 0) - (b.slotIndex ?? 0));

  if (
    whale.location.kind !== 'home' ||
    interactiveGuests.length === 0 ||
    (!isManualBanterActive && !autoBanterWindow)
  ) {
    return { hostSpeech: baseHostSpeech, guestSpeeches, banterMode: 'solo' };
  }

  const getWhaleName = (neighborId: string) =>
    snapshot.neighbors.find((n) => n.id === neighborId)?.whaleName ?? '邻家小鲸';

  // ==================== 场景 A：三鲸齐聚一堂（1 屋主 + 2 客鲸） ====================
  if (interactiveGuests.length >= 2 && !whale.isSleeping) {
    let gA = interactiveGuests[0];
    let gB = interactiveGuests[1];
    const bothWorking = gA.stage === 'working' && gB.stage === 'working';
    const bothSneaking = gA.stage === 'eating' && gB.stage === 'eating';

    // 若一打工一偷吃，确保 gA 指向打工鲸、gB 指向偷吃鲸以完美对齐台词角色！
    if (!bothWorking && !bothSneaking && gA.stage !== 'working') {
      [gA, gB] = [gB, gA];
    }

    const nameA = getWhaleName(gA.neighborId);
    const nameB = getWhaleName(gB.neighborId);

    const trioPool =
      home.troughGrams <= 0
        ? DIALOGUES.trioEmptyBowlBanter
        : bothWorking
          ? DIALOGUES.trioWorkPartyBanter
          : bothSneaking
            ? DIALOGUES.trioSneakPartyBanter
            : DIALOGUES.trioMixedPartyBanter;

    const script = trioPool[slotTick % trioPool.length];
    const vars = {
      host: whale.name,
      hostOutfit: whale.outfit,
      guestA: nameA,
      guestB: nameB,
    };

    guestSpeeches[gA.neighborId] = `💬 ${formatMultiWhaleLine(script.guestA, vars)}`;
    guestSpeeches[gB.neighborId] = `💬 ${formatMultiWhaleLine(script.guestB, vars)}`;

    return {
      hostSpeech: hasManualTemp ? baseHostSpeech : `💬 ${formatMultiWhaleLine(script.host, vars)}`,
      guestSpeeches,
      banterMode: 'trio_party',
    };
  }

  // ==================== 场景 B：双鲸同框互动（1 屋主 + 1 客鲸，或屋主睡觉时客鲸在场） ====================
  const partner = interactiveGuests[0];
  const partnerName = getWhaleName(partner.neighborId);

  const duoPool = whale.isSleeping
    ? partner.stage === 'working'
      ? DIALOGUES.duoSleepWorkBanter
      : DIALOGUES.duoSleepSneakBanter
    : partner.stage === 'working'
      ? DIALOGUES.duoWorkBanter
      : DIALOGUES.duoSneakBanter;

  const script = duoPool[slotTick % duoPool.length];
  const vars = { host: whale.name, hostOutfit: whale.outfit, guest: partnerName };

  guestSpeeches[partner.neighborId] = `💬 ${formatMultiWhaleLine(script.guest, vars)}`;
  return {
    hostSpeech: hasManualTemp ? baseHostSpeech : `💬 ${formatMultiWhaleLine(script.host, vars)}`,
    guestSpeeches,
    banterMode: partner.stage === 'working' ? 'duo_work' : 'duo_sneak',
  };
}
