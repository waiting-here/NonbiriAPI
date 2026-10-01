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
export function tutorialSteps(text: Translate): TutorialStep[] {
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
    text('likes.letSPlayAPracticeMatch'),
    text('likes.raceTo60LikesBothPlayersChoose'),
  );
  add(
    'loadout',
    'role:ChatGPT',
    text('likes.firstChooseChatGPT'),
    text('likes.selectTheHighlightedCharacterHerMultimodalPassive'),
  );
  add(
    'loadout',
    'harness:H01',
    text('likes.equipCodex'),
    text('likes.aHarnessDeterminesSkillSlotsAndFixed'),
  );
  const equipment = [
    ['PUB42', text('likes.usageReset'), text('likes.refillsBothSubscriptionsAndImagesSaveIt')],
    ['GPT01', text('likes.helloWorld'), text('likes.aStableBasicAttackRepeatedCastsBuild')],
    ['GPT41', text('likes.regenerate'), text('likes.spendAPIReserveToCleanseYourDebuffs')],
    ['GPT44', text('likes.pedalFaster'), text('likes.enableSpeedNextRoundTwiceTheLikes')],
    ['GPT61', text('likes.wordsAndPictures'), text('likes.anImageConsumingUltimateThatEarnsMany')],
  ];
  for (const [id, title, body] of equipment)
    add('loadout', `equip:${id}`, text('likes.equip', { title: title }), body);
  add(
    'loadout',
    'continue',
    text('likes.loadoutReady'),
    text('likes.allFiveSlotsAreReadyCardsShow'),
  );
  add(
    'matching',
    'continue',
    text('likes.matchDemoTeachingBotFound'),
    text('likes.liveMatchingReservesYourEntryCreditsAnd'),
  );
  const lessons = [
    [
      text('likes.readResourcesBeforeChoosing'),
      text('likes.theBatteryAboveIsSharedSubscriptionBurst'),
    ],
    [text('likes.cleanseToProtectYourLikes'), text('likes.theOpponentAppliedADebuffThatReduces')],
    [text('likes.shopBeforeCasting'), text('likes.upgradingAddsBurstTotalAndImageQuota')],
    [text('likes.theBatteryIsRunningLow'), text('likes.theOpponentSUltimateBuiltALarge')],
    [text('likes.counterWithAnUltimate'), text('likes.youHaveEnoughImagesAndSubscriptionQuota')],
    [text('likes.aResetHasTradeoffs'), text('likes.yourQuotasHaveFallenUsageResetCosts')],
    [text('likes.fallBehindNowToAccelerate'), text('likes.togglingSpeedScoresNoLikesThisRound')],
    [text('likes.yourFirstAcceleratedAttack'), text('likes.useHelloWorldAndWatchSpeedCombine')],
    [text('likes.cacheKeepsYouGoing'), text('likes.continueWithTheBasicAttackLastRound')],
    [text('likes.oneLastPush'), text('likes.youHaveANarrowLeadAndEnough')],
  ];
  for (let i = 0; i < tutorialData.rounds.length; i++) {
    const round = i + 1,
      plan = tutorialData.rounds[i].plan;
    add('plan', 'continue', lessons[i][0], lessons[i][1], round);
    if (plan.purchases.length) {
      add(
        'plan',
        'tab:shop',
        text('likes.openTheShop'),
        text('likes.selectTheHighlightedShopTab'),
        round,
      );
      const item = plan.purchases[0].item;
      add(
        'plan',
        `buy:${item}`,
        item === 'sub' ? text('likes.upgradeSubscription') : text('likes.chargeTheSharedBattery'),
        text('likes.addTheHighlightedPurchaseItResolvesWith'),
        round,
      );
      add(
        'plan',
        'tab:skills',
        text('likes.returnToSkills'),
        text('likes.thePurchaseIsInYourPlanNow'),
        round,
      );
    }
    add(
      'plan',
      `cast:${plan.main!.skillId}`,
      text('likes.chooseTheHighlightedSkill'),
      text('likes.choosingASkillDoesNotLockYour'),
      round,
    );
    if (plan.main!.pay === 'api')
      add(
        'plan',
        'payment',
        text('likes.useAPIThisRound'),
        text('likes.setPaymentToUseAPIReserve'),
        round,
        'api',
      );
    if (plan.main!.targets?.length)
      add(
        'plan',
        `target:${plan.main!.targets[0]}`,
        text('likes.chooseTheDebuffToCleanse'),
        text('likes.checkTheHighlightedDebuffToChooseWhat'),
        round,
      );
    add(
      'plan',
      'lock',
      text('likes.reviewAndLockYourPlan'),
      text('likes.youCannotChangeALockedPlanThe'),
      round,
    );
    add(
      'resolution',
      'resolution',
      text('likes.watchTheRoundResolve'),
      text('likes.shoppingPaymentCleansingLikesLaterChangesWatch'),
      round,
    );
    const scores = tutorialData.rounds[i].after.players.map((p) => p.likes);
    add(
      i === 9 ? 'finish' : 'review',
      'continue',
      i === 9
        ? text('likes.aHardFoughtVictory', { value: scores[0], value2: scores[1] })
        : text('likes.roundComplete', { value: scores[0], value2: scores[1] }),
      i === 9
        ? text('likes.youFellBehindCleansedReplenishedResourcesAnd')
        : text('likes.continueWhenYouHaveReviewedTheChanges'),
      round,
    );
  }
  return steps;
}
