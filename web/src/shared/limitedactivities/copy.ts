import { useRegisteredCopy } from '@shared/i18n/useRegisteredCopy';
import type { ActivityStatus, ActivityAsset } from './api';
const keys = {
  'common.schedule': 'common.limitedActivities.common.schedule',
  'common.nextScheduled': 'common.limitedActivities.common.nextScheduled',
  'common.nextPaused': 'common.limitedActivities.common.nextPaused',
  'common.nextEnded': 'common.limitedActivities.common.nextEnded',
  'common.nextUnavailable': 'common.limitedActivities.common.nextUnavailable',
  'common.nextUnconfigured': 'common.limitedActivities.common.nextUnconfigured',
  'common.nextOpen': 'common.limitedActivities.common.nextOpen',
  'common.notConfigured': 'common.limitedActivities.common.notConfigured',
  'common.temporarilyUnavailable': 'common.limitedActivities.common.temporarilyUnavailable',
  'common.notStarted': 'common.limitedActivities.common.notStarted',
  'common.open': 'common.limitedActivities.common.open',
  'common.paused': 'common.limitedActivities.common.paused',
  'common.ended': 'common.limitedActivities.common.ended',
  'common.sketchPaper': 'common.limitedActivities.common.sketchPaper',
  'common.paintBrushes': 'common.limitedActivities.common.paintBrushes',
  'common.fatFish': 'common.limitedActivities.common.fatFish',
  'common.editLevelsAndArrangePeriodsAnAdministrator':
    'common.limitedActivities.common.editLevelsAndArrangePeriodsAnAdministrator',
  'common.fatFishAdministration': 'common.limitedActivities.common.fatFishAdministration',
  'common.levelsAndVersions': 'common.limitedActivities.common.levelsAndVersions',
  'common.periodsAndNodes': 'common.limitedActivities.common.periodsAndNodes',
  'common.availabilityAndExchangeSettings':
    'common.limitedActivities.common.availabilityAndExchangeSettings',
  'common.hidingRemovesTheDirectoryEntryDirectLinks':
    'common.limitedActivities.common.hidingRemovesTheDirectoryEntryDirectLinks',
  'common.showInActivityDirectory': 'common.limitedActivities.common.showInActivityDirectory',
  'common.pauseActivity': 'common.limitedActivities.common.pauseActivity',
  'common.openingTime': 'common.limitedActivities.common.openingTime',
  'common.closingTimeExclusive': 'common.limitedActivities.common.closingTimeExclusive',
  'common.leaveBothTimesEmptyToKeepThe':
    'common.limitedActivities.common.leaveBothTimesEmptyToKeepThe',
  'common.generalCreditsPerSheet': 'common.limitedActivities.common.generalCreditsPerSheet',
  'common.generalCreditsPerBrush': 'common.limitedActivities.common.generalCreditsPerBrush',
  'common.totalCumulativeBrushExchangeCap':
    'common.limitedActivities.common.totalCumulativeBrushExchangeCap',
  'common.brushesAlreadyExchanged': 'common.limitedActivities.common.brushesAlreadyExchanged',
  'common.remaining': 'common.limitedActivities.common.remaining',
  'common.aCapBelowTheExchangedTotalStops':
    'common.limitedActivities.common.aCapBelowTheExchangedTotalStops',
  'common.theSaveResultIsUnconfirmedRetryThe':
    'common.limitedActivities.common.theSaveResultIsUnconfirmedRetryThe',
  'common.settingsSaved': 'common.limitedActivities.common.settingsSaved',
  'common.retrySave': 'common.limitedActivities.common.retrySave',
  'common.saveSettings': 'common.limitedActivities.common.saveSettings',
  'common.limitedTimeActivities': 'common.limitedActivities.common.limitedTimeActivities',
  'common.configureThePictureBookScheduleAndCurrency':
    'common.limitedActivities.common.configureThePictureBookScheduleAndCurrency',
  'common.manageLevelDraftsImmutableVersionsPlaytestsAnd':
    'common.limitedActivities.common.manageLevelDraftsImmutableVersionsPlaytestsAnd',
  'common.openLevelAndPeriodEditor': 'common.limitedActivities.common.openLevelAndPeriodEditor',
  'common.spaceKeycap': 'common.limitedActivities.common.spaceKeycap',
  'common.memoryModule': 'common.limitedActivities.common.memoryModule',
  'common.inferenceCard': 'common.limitedActivities.common.inferenceCard',
  'common.coolingFins': 'common.limitedActivities.common.coolingFins',
  'common.cachePuck': 'common.limitedActivities.common.cachePuck',
  'common.fatFishPlay': 'common.limitedActivities.common.fatFishPlay',
  'common.fatFishDinnerIsReady': 'common.limitedActivities.common.fatFishDinnerIsReady',
  'common.guideTheFishToRice': 'common.limitedActivities.common.guideTheFishToRice',
  'common.muteMusic': 'common.limitedActivities.common.muteMusic',
  'common.playMusic': 'common.limitedActivities.common.playMusic',
  'common.musicCouldNotPlayTryTurningIt':
    'common.limitedActivities.common.musicCouldNotPlayTryTurningIt',
  'common.thisChallengeIsActiveInAnotherTab':
    'common.limitedActivities.common.thisChallengeIsActiveInAnotherTab',
  'common.previewTheLayoutToolsCanMoveAfter':
    'common.limitedActivities.common.previewTheLayoutToolsCanMoveAfter',
  'common.startingShortly': 'common.limitedActivities.common.startingShortly',
  'common.theServerIsVerifyingTheResult':
    'common.limitedActivities.common.theServerIsVerifyingTheResult',
  'common.thisChallengeHasEnded': 'common.limitedActivities.common.thisChallengeHasEnded',
  'common.timeLeft': 'common.limitedActivities.common.timeLeft',
  'common.rescued': 'common.limitedActivities.common.rescued',
  'common.lost': 'common.limitedActivities.common.lost',
  'common.dinnerWorkshop': 'common.limitedActivities.common.dinnerWorkshop',
  'common.dragPiecesFreelyBetweenTheFieldAnd':
    'common.limitedActivities.common.dragPiecesFreelyBetweenTheFieldAnd',
  'common.fitScreen': 'common.limitedActivities.common.fitScreen',
  'common.enlargeField': 'common.limitedActivities.common.enlargeField',
  'common.onTouchScreensDragEmptySpaceTo':
    'common.limitedActivities.common.onTouchScreensDragEmptySpaceTo',
  'common.chooseAPieceOrDragItDirectly':
    'common.limitedActivities.common.chooseAPieceOrDragItDirectly',
  'common.noneSelected': 'common.limitedActivities.common.noneSelected',
  'common.piece': 'common.limitedActivities.common.piece',
  'common.keyboardFocusTheFieldPressNTo':
    'common.limitedActivities.common.keyboardFocusTheFieldPressNTo',
  'common.returnTool': 'common.limitedActivities.common.returnTool',
  'common.finishWithTheCurrentRescuedCountRemaining':
    'common.limitedActivities.common.finishWithTheCurrentRescuedCountRemaining',
  'common.finish': 'common.limitedActivities.common.finish',
  'common.submitForVerification': 'common.limitedActivities.common.submitForVerification',
  'common.abandonThisChallengeAStartedTicketIs':
    'common.limitedActivities.common.abandonThisChallengeAStartedTicketIs',
  'common.abandon': 'common.limitedActivities.common.abandon',
  'common.localEstimateOnly': 'common.limitedActivities.common.localEstimateOnly',
  'common.verified': 'common.limitedActivities.common.verified',
  'common.completeAHiddenPrerequisite':
    'common.limitedActivities.common.completeAHiddenPrerequisite',
  'common.noPrerequisite': 'common.limitedActivities.common.noPrerequisite',
  'common.passNode': 'common.limitedActivities.common.passNode',
  'common.reachStars': 'common.limitedActivities.common.reachStars',
  'common.meetAll': 'common.limitedActivities.common.meetAll',
  'common.meetAny': 'common.limitedActivities.common.meetAny',
  'common.hiddenNode': 'common.limitedActivities.common.hiddenNode',
  'common.node': 'common.limitedActivities.common.node',
  'common.challengeResumedInThisTab': 'common.limitedActivities.common.challengeResumedInThisTab',
  'common.thisTabCannotResumeTheChallengeYou':
    'common.limitedActivities.common.thisTabCannotResumeTheChallengeYou',
  'common.unlockForGeneralCredits': 'common.limitedActivities.common.unlockForGeneralCredits',
  'common.nodeUnlocked': 'common.limitedActivities.common.nodeUnlocked',
  'common.readyConfirmTheTicketChargeToStart':
    'common.limitedActivities.common.readyConfirmTheTicketChargeToStart',
  'common.startingChargesGeneralCreditsPreparationIsFree':
    'common.limitedActivities.common.startingChargesGeneralCreditsPreparationIsFree',
  'common.abandonThisChallengeAChargedTicketIs':
    'common.limitedActivities.common.abandonThisChallengeAChargedTicketIs',
  'common.limitedTimeActivity': 'common.limitedActivities.common.limitedTimeActivity',
  'common.raiseABigFish': 'common.limitedActivities.common.raiseABigFish',
  'common.placeToolsAndGuideTheFishTo':
    'common.limitedActivities.common.placeToolsAndGuideTheFishTo',
  'common.backToActivities': 'common.limitedActivities.common.backToActivities',
  'common.currentChallenge': 'common.limitedActivities.common.currentChallenge',
  'common.status': 'common.limitedActivities.common.status',
  'common.ticket': 'common.limitedActivities.common.ticket',
  'common.confirmTicketAndStart': 'common.limitedActivities.common.confirmTicketAndStart',
  'common.thisTabCannotContinuePlayingWaitFor':
    'common.limitedActivities.common.thisTabCannotContinuePlayingWaitFor',
  'common.abandonChallenge': 'common.limitedActivities.common.abandonChallenge',
  'common.periods': 'common.limitedActivities.common.periods',
  'common.open2': 'common.limitedActivities.common.open2',
  'common.endedOrPaused': 'common.limitedActivities.common.endedOrPaused',
  'common.previous': 'common.limitedActivities.common.previous',
  'common.next': 'common.limitedActivities.common.next',
  'common.newChallengesArePaused': 'common.limitedActivities.common.newChallengesArePaused',
  'common.mapZoom': 'common.limitedActivities.common.mapZoom',
  'common.zoomOut': 'common.limitedActivities.common.zoomOut',
  'common.zoomIn': 'common.limitedActivities.common.zoomIn',
  'common.nodeMap': 'common.limitedActivities.common.nodeMap',
  'common.unlocked': 'common.limitedActivities.common.unlocked',
  'common.eligible': 'common.limitedActivities.common.eligible',
  'common.locked': 'common.limitedActivities.common.locked',
  'common.unlockCost': 'common.limitedActivities.common.unlockCost',
  'common.ticketPerChallenge': 'common.limitedActivities.common.ticketPerChallenge',
  'common.firstClearReward': 'common.limitedActivities.common.firstClearReward',
  'common.starRewards': 'common.limitedActivities.common.starRewards',
  'common.initialLayout': 'common.limitedActivities.common.initialLayout',
  'common.personalBest': 'common.limitedActivities.common.personalBest',
  'common.unlockNode': 'common.limitedActivities.common.unlockNode',
  'common.prepareChallengeFree': 'common.limitedActivities.common.prepareChallengeFree',
  'common.thisBrowserCannotSafelySaveAndExclusively':
    'common.limitedActivities.common.thisBrowserCannotSafelySaveAndExclusively',
  'common.leaderboard': 'common.limitedActivities.common.leaderboard',
  'common.scope': 'common.limitedActivities.common.scope',
  'common.periodTotal': 'common.limitedActivities.common.periodTotal',
  'common.anonymousPlayer': 'common.limitedActivities.common.anonymousPlayer',
  'common.me': 'common.limitedActivities.common.me',
  'common.challengeHistory': 'common.limitedActivities.common.challengeHistory',
  'common.passed': 'common.limitedActivities.common.passed',
  'common.notPassed': 'common.limitedActivities.common.notPassed',
  'common.refund': 'common.limitedActivities.common.refund',
  'common.rewards': 'common.limitedActivities.common.rewards',
  'common.noLimitedTimeActivitiesAreListed':
    'common.limitedActivities.common.noLimitedTimeActivitiesAreListed',
  'common.pictureBook': 'common.limitedActivities.common.pictureBook',
  'common.rengeCarefullyDrawingASimplePictureOn':
    'common.limitedActivities.common.rengeCarefullyDrawingASimplePictureOn',
  'common.fatFishMoveObstaclesAndHappilyHead':
    'common.limitedActivities.common.fatFishMoveObstaclesAndHappilyHead',
  'common.openThePictureBookAndCollectSketch':
    'common.limitedActivities.common.openThePictureBookAndCollectSketch',
  'common.moveObstaclesAndGuideHungryFatFish':
    'common.limitedActivities.common.moveObstaclesAndGuideHungryFatFish',
  'common.viewActivity': 'common.limitedActivities.common.viewActivity',
  'common.balancesAndRecordsContinueWhenTheActivity':
    'common.limitedActivities.common.balancesAndRecordsContinueWhenTheActivity',
  'common.activityWalletAndExchange': 'common.limitedActivities.common.activityWalletAndExchange',
  'common.generalCredits': 'common.limitedActivities.common.generalCredits',
  'common.brushesRemainingAcrossTheSite':
    'common.limitedActivities.common.brushesRemainingAcrossTheSite',
  'common.exchangeGeneralCreditsForActivityCurrencyExchanges':
    'common.limitedActivities.common.exchangeGeneralCreditsForActivityCurrencyExchanges',
  'common.currency': 'common.limitedActivities.common.currency',
  'common.quantity': 'common.limitedActivities.common.quantity',
  'common.generalCreditsCharged': 'common.limitedActivities.common.generalCreditsCharged',
  'common.thePreviousResultIsUnconfirmedRetryThe':
    'common.limitedActivities.common.thePreviousResultIsUnconfirmedRetryThe',
  'common.retryTheSameExchange': 'common.limitedActivities.common.retryTheSameExchange',
  'common.confirmExchange': 'common.limitedActivities.common.confirmExchange',
  'common.exchanged': 'common.limitedActivities.common.exchanged',
} as const;
export function useActivityText() {
  return useRegisteredCopy(keys).t;
}
export type ActivityText = ReturnType<typeof useActivityText>;
export function statusLabel(status: ActivityStatus, text: ActivityText) {
  return {
    unconfigured: text('common.notConfigured'),
    unavailable: text('common.temporarilyUnavailable'),
    scheduled: text('common.notStarted'),
    open: text('common.open'),
    paused: text('common.paused'),
    ended: text('common.ended'),
  }[status];
}
export function currencyLabel(asset: ActivityAsset, text: ActivityText) {
  return asset === 'sketch_paper' ? text('common.sketchPaper') : text('common.paintBrushes');
}
