import type { TFunction } from 'i18next';
export function accountCopy(t: TFunction) {
  return {
    deleted: t('common.account.deleted'),
    allAccounts: t('common.account.allAccounts'),
    activeAccounts: t('common.account.activeAccounts'),
    deletedAccounts: t('common.account.deletedAccounts'),
    discordInvalid: t('common.account.discordInvalid'),
    close: t('common.account.close'),
    unknown: t('common.account.unknown'),
    formerID: t('common.account.formerID'),
    discord: t('common.account.discord'),
    registered: t('common.account.registered'),
    deletedAt: t('common.account.deletedAt'),
    level: t('common.account.level'),
    source: t('common.account.source'),
    ban: t('common.account.ban'),
    pause: t('common.account.pause'),
    active: t('common.account.active'),
    inactive: t('common.account.inactive'),
    until: t('common.account.until'),
    reason: t('common.account.reason'),
    blacklist: t('common.account.blacklist'),
    general: t('common.account.general'),
    game: t('common.account.game'),
    donation: t('common.account.donation'),
    paper: t('common.account.paper'),
    brush: t('common.account.brush'),
    sameIdentity: t('common.account.sameIdentity'),
    alert: t('common.account.alert'),
    aborts: t('common.account.aborts'),
    noAborts: t('common.account.noAborts'),
    previous: t('common.account.previous'),
    next: t('common.account.next'),
    match: t('common.account.match'),
    occurred: t('common.account.occurred'),
  };
}
export function accountHistoryCopy(t: TFunction) {
  return {
    coverage: t('common.accountHistory.coverage'),
    sources: {
      self: t('common.accountHistory.sources.self'),
      admin: t('common.accountHistory.sources.admin'),
      system: t('common.accountHistory.sources.system'),
      unknown: t('common.accountHistory.sources.unknown'),
    },
    actions: {
      none: t('common.accountHistory.actions.none'),
      added: t('common.accountHistory.actions.added'),
      appended: t('common.accountHistory.actions.appended'),
      unknown: t('common.accountHistory.actions.unknown'),
    },
    reasons: {
      deletion_penalty_evasion: t('common.accountHistory.reasons.deletion_penalty_evasion'),
      deletion_debt_evasion: t('common.accountHistory.reasons.deletion_debt_evasion'),
    },
    games: {
      bidding: t('common.accountHistory.games.bidding'),
      likes: t('common.accountHistory.games.likes'),
      gwent: t('common.accountHistory.games.gwent'),
    },
  };
}
