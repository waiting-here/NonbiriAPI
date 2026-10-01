import type { Buff, Effect, ModeCatalog, Passive, Skill } from './catalog';
import type { Translate } from './labels';
import { characterPassive, effectCategory } from './characterPassives';
export type GuideLevel = 'base' | 'I' | 'II';
export interface GuideEntry {
  id: string;
  title: string;
  summary: string;
  paragraphs: string[];
  meme: string;
  refs: string[];
}
const currentBalance = (c: ModeCatalog) => c.designVersion === '0.19.0';
const link = (id: string) => `[[${id}]]`;
const references = (text: string) =>
  [...text.matchAll(/\[\[([^\]]+)\]\]/g)].map((match) => match[1]);
export const guideLevels: readonly GuideLevel[] = ['base', 'I', 'II'];
export function levelName(level: GuideLevel, text: Translate) {
  return level === 'base' ? text('likes.original') : `${text('likes.distilled')} ${level}`;
}
export function entryTitle(c: ModeCatalog, id: string, text: Translate): string {
  const character = c.roles.find((r) => r.passive?.id === id);
  if (character) return characterPassive(character, text)!.name;
  const entry = [...c.skills, ...c.buffs, ...c.harnesses, ...c.passives].find(
    (item) => item.id === id,
  );
  if (!entry) return text('likes.entryNotFound');
  if (!c.buffs.some((item) => item.id === id)) return entry.name;
  const suffix = id.split(':')[1];
  const version =
    suffix === '原版'
      ? text('likes.original')
      : suffix === '高阶'
        ? text('likes.distilledII')
        : suffix === '低阶'
          ? text('likes.distilledI')
          : suffix === '原版/高阶'
            ? text('likes.originalDistilledII')
            : '';
  return version ? `${entry.name} · ${version}` : entry.name;
}
function buffText(c: ModeCatalog, b: Buff, text: Translate): string[] {
  const { p, q, n, cap, kind } = b;
  const timed: Record<string, string> = {
    AMPLIFY: text('likes.eachMainOrPreselectedExtraSkillWith', { p: p }),
    SUPPRESS: text('likes.eachMainOrPreselectedExtraSkillWith2', { p: p }),
    TOKEN_TAX: text('likes.eachMainAndPreselectedExtraSkillCosts', { p: p }),
    NONBASIC_TAX: text('likes.eachNonBasicMainOrPreselectedExtra', { p: p }),
    SAVE_ENERGY: text('likes.eachMainAndPreselectedExtraSkillSaves', {
      p: p,
      ENERGY_FLOOR: c.parameters.ENERGY_FLOOR,
    }),
    API_DISCOUNT: text('likes.aPIOnlySkillsMixedSkillsExplicitlySet', {
      p: p,
      TOKEN_FLOOR: c.parameters.TOKEN_FLOOR,
    }),
    REGULATOR: text('likes.duringTheNextRoundEachMainAnd', {
      REGULATOR_SAVE: c.parameters.REGULATOR_SAVE,
      ENERGY_FLOOR: c.parameters.ENERGY_FLOOR,
    }),
  };
  if (timed[kind])
    return [
      timed[kind],
      text('likes.startsNextRoundAndLastsRoundsTriggered', { n: n }),
      b.reapply === 'add'
        ? text('likes.reapplyingAddsRoundsAfterThisRoundS', { n: n, cap: cap })
        : text('likes.reapplyingRefreshesTheDurationToRoundsWithout', { n: n }),
      text('likes.distinctEntriesAndSeparatelyNamedOriginalDistilled'),
    ];
  switch (kind) {
    case 'CACHE': {
      const scope = b.cacheScope ?? '';
      const target = scope.endsWith('flash')
        ? text('likes.flash')
        : scope.endsWith('pro')
          ? text('likes.pro')
          : text('likes.catgirlRoleplayHelloWorldPoliteReplyFormatted');
      return [
        text('likes.eachLayerSavesKTokensOnUp', {
          value: scope.startsWith('distilled') ? text('likes.distilled2') : text('likes.original2'),
          target: target,
          p: p,
          cap: cap,
          TOKEN_FLOOR: c.parameters.TOKEN_FLOOR,
        }),
        text('likes.newLayersMergeWithThisCacheAcquiring', {
          value: scope === 'flash' ? text('likes.layersEarnedByTriggeredFlashAreImmediately') : '',
        }),
        text('likes.ordinaryLayersExpireAtRoundEndIf', {
          value: b.refreshOnCombo ? text('likes.andNoFollowUpProgress') : '',
        }),
        text('likes.layersMadePersistentBySurviveExpirySkips', { link: link('PUB22'), cap: cap }),
      ];
    }
    case 'ENERGY_STACK':
      return [
        text('likes.eachLayerSavesEnergyOnMainAnd', {
          p: p,
          cap: cap,
          ENERGY_FLOOR: c.parameters.ENERGY_FLOOR,
        }),
        text('likes.newLayersStartNextRoundAndPersist'),
      ];
    case 'COMBO':
      return [
        text('likes.everyLayersAreSpentToAttemptOriginal', { p: p, q: q, link: link('GEM01') }),
        text('likes.successfulFollowUpsGrantProgressAndOriginal'),
      ];
    case 'STUN':
      return [
        text('likes.stunnedForRoundStartingNextRoundSkills', { n: n }),
        text('likes.canBeCleansedReapplyingRefreshesItsDuration'),
      ];
    case 'OVERLOAD':
      return [
        ...(currentBalance(c) ? [text('likes.insufficientImageQuotaAlsoCausesOverloadThe')] : []),
        text('likes.failedCastingFromInsufficientEnergyOrTokens', {
          n: n,
          link: link('B34:状态'),
          value: c.buffs.find((v) => v.kind === 'SPEED_MODE')?.n ?? 2,
        }),
        text('likes.cannotBeCleansedOrDispelledSubscriptionClocks'),
      ];
    case 'SPEED_MODE':
      return [
        currentBalance(c)
          ? text('likes.baseTokenAndEnergyCostsAreMultiplied')
          : text('likes.baseTokenAndEnergyCostsAreMultiplied2', { p: p, q: q }),
        text('likes.appliesToMainSkillsDistillationExtraSkills', { link: link('GPT44'), n: n }),
      ];
    case 'SUBSCRIPTION_BAN':
      return [
        text('likes.fromNextRoundSubscriptionBurstTotalAnd'),
        text('likes.upgradesResetsAndClocksContinueCleansingRestores'),
      ];
    case 'SOTA_FANATICISM':
      return [
        text('likes.eachLayerAddsBaseEnergyToSkills', { p: p, cap: cap }),
        text('likes.canBeCleansedLosesOneLayerAt'),
      ];
    case 'BASE_SUPPRESS':
      return [
        text('likes.eachLayerReducesBaseLikesByFor', { p: p, cap: cap }),
        text('likes.lastsUntilCleansedOriginalAndDistilledStacks'),
      ];
    case 'MODEL_DEGRADATION':
      return [
        text('likes.startingNextRoundEverySuccessfulSkillLoses', { p: p, cap: cap }),
        text('likes.doesNotExpireNaturallyAndCanBe'),
      ];
    default:
      throw new Error(`Unsupported effect description: ${kind}`);
  }
}
function passiveText(c: ModeCatalog, p: Passive, text: Translate): string[] {
  switch (p.kind) {
    case 'LIKE_STRENGTH':
      return [
        text('likes.skillsWithPositiveBaseTokenCostEnergy', { p: p.p, q: p.q }),
        text('likes.thisBonusIsPartOfTheBase'),
      ];
    case 'SOTA_ONLY':
      return [
        ...(currentBalance(c) ? [text('likes.eachDistinctDebuffSuccessfullyAppliedToThe')] : []),
        text('likes.eachDistinctNegativeEffectASuccessfulSkill', { p: p.p, link: link(p.buffId!) }),
        text('likes.applicationAtTheCapStillCountsThe'),
      ];
    case 'PRO_EXPERIENCE':
      return [
        text('likes.eachSuccessfulBasicAttackGrantsExtraLike', { p: p.p }),
        text('likes.theExtraLikesBypassAdditiveBuffsAnd'),
      ];
    case 'FREE_TRIAL':
      return [
        text('likes.eachSuccessfulNonBasicSkillCastBy', { p: p.p }),
        text('likes.trialQuotaPaysFinalTokenCostsBefore'),
      ];
    case 'API_SPECIALIST':
      return [
        text('likes.eachSuccessfulSkillLabelledAPIOnlyGrants', { p: p.p }),
        text('likes.mixedSkillsDoNotQualifyEvenWhen'),
      ];
    case 'VALUE_SUBSCRIPTION':
      return [text('likes.subscriptionUpgradesCostLessGoldToA', { p: p.p })];
    case 'STUDENT_DISCOUNT':
      return [
        text('likes.whenStrictlyBehindOnLikesAtRound', { p: p.p }),
        text('likes.theConditionIsNotReevaluatedDuringThe'),
      ];
    case 'CODE_COMPLETION':
      return [text('likes.allSkillsSaveKBaseTokensTo', { p: p.p })];
    default:
      throw new Error(`Unsupported passive description: ${p.kind}`);
  }
}
function effectText(c: ModeCatalog, s: Skill, level: GuideLevel, text: Translate): string[] {
  const f: Effect = { ...s.effects[level] };
  const nominalToken =
    level === 'base' ? s.token : c.skills.find((item) => item.id === 'PUB41')!.token;
  const b = c.buffs.find((item) => item.id === f.buffId);
  if (
    b &&
    ['AMPLIFY', 'SUPPRESS', 'TOKEN_TAX', 'NONBASIC_TAX', 'SAVE_ENERGY', 'API_DISCOUNT'].includes(
      f.kind,
    )
  ) {
    f.p = b.p;
    f.n = b.n;
  }
  const out: string[] = [];
  if (s.id === 'PUB41')
    return [
      text('likes.castThePreviouslyLearnedTemplateFirstThen', { learn: s.learn }),
      text('likes.aNewTemplateStartsAtDistilledI'),
      text('likes.everyTemplateUsesDistillationSOwnToken'),
    ];
  out.push(text('likes.gainBaseLikes', { likes: f.likes }));
  const grant = b ? link(b.id) : '';
  switch (f.kind) {
    case 'SCORE':
      break;
    case 'CACHE':
    case 'CACHE_COMBO':
      if (b && f.n > 0) out.push(text('likes.onASuccessfulCastGainOneLayer', { grant: grant }));
      if (f.extraBuffId && f.n > 0)
        out.push(text('likes.alsoGainOneLayerOf', { link: link(f.extraBuffId) }));
      break;
    case 'SVG_DECAY':
      out.push(text('likes.afterEachSuccessThisVersionSNext', { p: f.p, q: f.q }));
      break;
    case 'PERSIST_CACHE':
      out.push(text('likes.afterCleansingAndDispellingMakeTheCache'));
      break;
    case 'AMPLIFY':
    case 'SAVE_ENERGY':
    case 'API_DISCOUNT':
      out.push(
        text('likes.gainForRoundsStartingNextRound', {
          grant: grant,
          n: f.n,
          value: buffText(c, b!, text)[0],
        }),
      );
      break;
    case 'SUPPRESS':
    case 'TOKEN_TAX':
    case 'NONBASIC_TAX':
      out.push(
        text('likes.giveTheOpponentForRoundsStartingNext', {
          grant: grant,
          n: f.n,
          value: buffText(c, b!, text)[0],
        }),
      );
      break;
    case 'CLEANSE':
    case 'DISPEL':
    case 'CLEANSE_OR_DISPEL': {
      const target =
        f.kind === 'CLEANSE'
          ? text('likes.yourDebuffs')
          : f.kind === 'DISPEL'
            ? text('likes.theOpponentSBuffs')
            : text('likes.yourDebuffsOrTheOpponentSBuffs');
      out.push(
        text('likes.upToOfRemovalHappensBeforeEither', {
          value: f.randomTargets ? text('likes.randomlyRemove') : text('likes.chooseAndRemove'),
          p: f.p,
          target: target,
        }),
      );
      out.push(text('likes.doesNotRemoveEnergyTokensOrImage'));
      break;
    }
    case 'COMBO':
      out.push(text('likes.gainLayersOfWhichMayTriggerFollow', { p: f.p, grant: grant }));
      break;
    case 'INSERT':
      out.push(
        text('likes.preselectUpToExtraSkillInThis', {
          value: Math.min(f.p, c.parameters.INSERT_CAP),
        }),
      );
      break;
    case 'PREDICT_COUNTER':
      out.push(text('likes.ifThisSkillSucceedsAndTheOpponent', { p: f.p, q: f.q }));
      break;
    case 'AUDIT':
      out.push(
        text('likes.gainAdditionalLikesIfSelectedMainSkill', {
          value: f.auditTarget === 'self' ? text('likes.your') : text('likes.theOpponentS'),
          p: f.p,
        }),
      );
      break;
    case 'DUAL_AUDIT':
      out.push(text('likes.gainExtraLikesIfYouSuccessfullyCast', { p: f.p, q: f.q }));
      break;
    case 'LOW_POWER':
      out.push(text('likes.gainExtraLikesWhenSharedEnergyAfter', { p: f.p, q: f.q }));
      break;
    case 'BURST_DRAIN':
      out.push(text('likes.afterSkillPaymentDrainKOfThe', { p: f.p }));
      break;
    case 'SELF_OVERLOAD':
      out.push(text('likes.overloadYourselfFor1RoundStartingNext'));
      break;
    case 'SELF_STUN':
      out.push(text('likes.becomeAffectedByForRoundStartingNext', { n: f.n, grant: grant }));
      break;
    case 'ENERGY_STACK':
      out.push(
        text('likes.gainLayersOfFromNextRoundEach', {
          n: f.n,
          grant: grant,
          value: b?.p ?? f.p,
          value2: b?.cap ?? f.q,
        }),
      );
      break;
    case 'APOLOGY':
      out.push(text('likes.applyLayersOfToYourselfAndTo', { p: f.p, q: f.q, grant: grant }));
      break;
    case 'DEGRADE':
      out.push(text('likes.giveTheOpponentLayersOfPlusWhen', { p: f.p, grant: grant, q: f.q }));
      break;
    case 'RESOURCE_GAIN':
      out.push(text('likes.afterMainExtraAndFlashPaymentsGain', { p: f.p, q: f.q }));
      break;
    case 'CACHE_CONVERT':
      out.push(
        text('likes.convertAllOriginalProCacheIncludingThis', {
          p: f.p,
          value: Math.floor((nominalToken * f.p) / 100),
          q: f.q,
          link: link('B28:通用'),
        }),
      );
      break;
    case 'USAGE_RESET':
      out.push(text('likes.afterAllSkillPaymentsAndBurstDrains'));
      break;
    case 'TOGGLE_SPEED':
      out.push(text('likes.toggleYourAtRoundEndForThe', { grant: grant }));
      break;
    case 'SUBSCRIPTION_BAN':
      out.push(
        text('likes.giveTheOpponentStartingNextRound', {
          grant: grant,
          value:
            f.n > 0 ? text('likes.lastingRound', { n: f.n }) : text('likes.lastingUntilCleansed'),
        }),
      );
      break;
    default:
      throw new Error(`Unsupported skill description: ${f.kind}`);
  }
  if (f.combo) out.push(text('likes.alsoGainLayersOf', { combo: f.combo, link: link('B28:通用') }));
  return out;
}
export function knowledge(
  c: ModeCatalog,
  id: string,
  level: GuideLevel,
  text: Translate,
): GuideEntry {
  const role = c.roles.find((r) => r.passive?.id === id);
  if (role) {
    const content = characterPassive(role, text)!;
    return {
      id,
      title: content.name,
      summary: content.description,
      paragraphs: [
        content.description,
        text('likes.alwaysActiveWithoutEquipmentAlongsideHarnessPassives'),
      ],
      meme: '',
      refs: [],
    };
  }
  const skill = c.skills.find((s) => s.id === id),
    buff = c.buffs.find((b) => b.id === id),
    harness = c.harnesses.find((h) => h.id === id),
    passive = c.passives.find((p) => p.id === id);
  let paragraphs: string[] = [],
    meme = '';
  if (skill) {
    paragraphs = effectText(c, skill, level, text);
    const owner = c.roles.find((r) => r.id === skill.owner);
    if (owner?.passive)
      paragraphs.push(`${link(owner.passive.id)}：${characterPassive(owner, text)!.description}`);
    meme = skill.effects[level].meme ?? skill.meme;
  } else if (buff) {
    paragraphs = [
      ...buffText(c, buff, text),
      `${text('likes.classification')}${effectCategory(buff, text)}`,
    ];
    if (buff.category === 'debuff' && c.roles.some((r) => r.passive))
      paragraphs.push(text('likes.eachHostileLayerChecksHitIndependentlySelf'));
    meme = buff.meme ?? '';
  } else if (passive) {
    paragraphs = passiveText(c, passive, text);
    if (passive.kind === 'SOTA_ONLY' && c.roles.some((r) => r.passive))
      paragraphs.push(text('likes.atLeastOneSuccessfulLayerOfA'));
    meme = passive.meme;
  } else if (harness) {
    paragraphs = [
      text('likes.addsActiveSlotsForTotalFixedPassives', {
        activeSlots: harness.activeSlots,
        value: 4 + harness.activeSlots,
      }),
      ...harness.passives.map(
        (pid) =>
          `${link(pid)}：${
            passiveText(
              c,
              c.passives.find((p) => p.id === pid)!,
              text,
            )[0]
          }`,
      ),
    ];
    meme = harness.meme;
  }
  const summary = skill
    ? skillBrief(c, skill, level, text)
    : harness
      ? harness.passives
          .map((pid) =>
            passiveBrief(
              c,
              c.passives.find((p) => p.id === pid)!,
              text,
            ),
          )
          .join(' ') || paragraphs[0]
      : passive
        ? passiveBrief(c, passive, text)
        : buff
          ? buffBrief(c, buff, text) || paragraphs[0]
          : (paragraphs[0] ?? '');
  return {
    id,
    title: entryTitle(c, id, text),
    summary,
    paragraphs,
    meme,
    refs: [...new Set(paragraphs.flatMap(references))],
  };
}
function passiveBrief(c: ModeCatalog, p: Passive, text: Translate): string {
  const briefs: Record<string, string> = {
    LIKE_STRENGTH: text('likes.skillsCostingEnergyAndTokensWithPositive', { p: p.p, q: p.q }),
    SOTA_ONLY: currentBalance(c)
      ? text('likes.eachSuccessfulDistinctHostileDebuffGrants1')
      : text('likes.eachAppliedDebuffAddsFanaticismLayers', { p: p.p }),
    PRO_EXPERIENCE: text('likes.successfulBasicsGainLikesIncludingFlashFollow', { p: p.p }),
    FREE_TRIAL: text('likes.eachSuccessfulOpponentNonBasicGrantsK', { p: p.p }),
    API_SPECIALIST: text('likes.successfulAPIOnlySkillsGainLikes', { p: p.p }),
    VALUE_SUBSCRIPTION: text('likes.subscriptionUpgradesSaveGold', { p: p.p }),
    STUDENT_DISCOUNT: text('likes.whenBehindAtRoundStartSkillsWith', { p: p.p }),
    CODE_COMPLETION: text('likes.baseSkillTokensKMinimum0', { p: p.p }),
  };
  return briefs[p.kind] ?? passiveText(c, p, text)[0];
}
function buffBrief(c: ModeCatalog, b: Buff, text: Translate): string {
  if (b.kind === 'CACHE')
    return text('likes.matchingBasicsSaveKTokensPerLayer', { p: b.p, cap: b.cap });
  if (b.kind === 'OVERLOAD')
    return text('likes.automaticallySkipShoppingAndCastingWhileRecovering');
  if (b.kind === 'SPEED_MODE')
    return currentBalance(c)
      ? text('likes.baseEnergyAndTokens25Rounded')
      : text('likes.baseEnergyAndTokensLikes', { p: b.p, q: b.q });
  if (b.kind === 'API_DISCOUNT') return text('likes.qualifyingAPICastsSaveKTokens', { p: b.p });
  return '';
}
function skillBrief(c: ModeCatalog, s: Skill, level: GuideLevel, text: Translate): string {
  if (s.id === 'PUB41') return text('likes.castYourLearnedTemplateThenLearnThe');
  const f = s.effects[level],
    b = c.buffs.find((item) => item.id === f.buffId);
  const extra: Record<string, string> = {
    CACHE: b && f.n ? text('likes.cacheSavesKTokensPerLayerUp', { p: b.p, cap: b.cap }) : '',
    CACHE_COMBO: b && f.n ? text('likes.gainFlashCacheAndFollowUpProgress') : '',
    SVG_DECAY: text('likes.eachSuccessReducesFutureBaseLikesBy', { p: f.p, q: f.q }),
    PERSIST_CACHE: text('likes.makeExistingCachePersistent'),
    AMPLIFY: text('likes.gainLikesPerEligibleCastForRounds', {
      value: b?.p ?? f.p,
      value2: b?.n ?? f.n,
    }),
    SUPPRESS: text('likes.opponentLosesLikesPerEligibleCastFor', {
      value: b?.p ?? f.p,
      value2: b?.n ?? f.n,
    }),
    SAVE_ENERGY: text('likes.saveEnergyPerChosenSkillForRounds', {
      value: b?.p ?? f.p,
      value2: b?.n ?? f.n,
    }),
    TOKEN_TAX: text('likes.opponentPaysKExtraTokensPerChosen', { value: b?.p ?? f.p }),
    NONBASIC_TAX: text('likes.opponentSNonBasicSkillsCostK', { value: b?.p ?? f.p }),
    API_DISCOUNT: text('likes.qualifyingAPISkillsSaveKTokensFrom', { value: b?.p ?? f.p }),
    CLEANSE: text('likes.cleanseUpToOfYourDebuffs', { p: f.p }),
    DISPEL: text('likes.dispelUpToOpponentBuffs', { p: f.p }),
    CLEANSE_OR_DISPEL: text('likes.cleanseYourselfOrDispelOpponentBuffsUp', { p: f.p }),
    COMBO: text('likes.gainFollowUpProgress', { p: f.p }),
    INSERT: text('likes.preselectExtraSkill', { p: f.p }),
    PREDICT_COUNTER: text('likes.predictANonBasicMainYouGain', { q: f.q, p: f.p }),
    AUDIT: text('likes.gainMoreIfPreviousMainWasA', {
      value: f.auditTarget === 'self' ? text('likes.your') : text('likes.theOpponentS'),
      p: f.p,
    }),
    DUAL_AUDIT: text('likes.yourOpponentSSuccessfulNonBasicCasts', { p: f.p, q: f.q }),
    LOW_POWER: text('likes.gainMoreWhenPrePaymentEnergyIs', { p: f.p, q: f.q }),
    BURST_DRAIN: text('likes.drainKOpponentBurst', { p: f.p }),
    SELF_OVERLOAD: text('likes.overloadYourselfNextRoundFor1Round'),
    SELF_STUN: text('likes.stunYourselfForRoundStartingNextRound', { n: f.n }),
    ENERGY_STACK: text('likes.gainLastingEnergySavingLayersSavingEach', {
      n: f.n,
      value: b?.p ?? f.p,
    }),
    APOLOGY: text('likes.applyBaseSuppressionLayersToYourselfOpponent', { p: f.p, q: f.q }),
    DEGRADE: text('likes.applyDegradationLayersPlusIfBehindAt', { p: f.p, q: f.q }),
    RESOURCE_GAIN: text('likes.afterPaymentGainGoldAndKAPI', { p: f.p, q: f.q }),
    CACHE_CONVERT: text('likes.convertProCacheToFlashGainingAPI'),
    USAGE_RESET: text('likes.refillBothSubscriptionsAndImagesDoesNot'),
    TOGGLE_SPEED: text('likes.toggleSpeedNextRoundBaseCostsLikes', {
      p: String(b?.p),
      q: String(b?.q),
    }),
    SUBSCRIPTION_BAN: text('likes.banOpponentSubscriptionAndImagesFromNext'),
  };
  return `${text('likes.baseLikes', { likes: f.likes })} ${extra[f.kind] ?? ''}${f.combo ? text('likes.alsoGainFollowUpProgress', { combo: f.combo }) : ''}`.trim();
}
export function relatedEntries(c: ModeCatalog, root: GuideEntry, text: Translate): GuideEntry[] {
  const seen = new Set([root.id]),
    queue = [...root.refs],
    result: GuideEntry[] = [];
  while (queue.length) {
    const id = queue.shift()!;
    if (seen.has(id)) continue;
    seen.add(id);
    const entry = knowledge(c, id, 'base', text);
    if (!entry.paragraphs.length) continue;
    result.push(entry);
    queue.push(...entry.refs);
  }
  return result;
}
