export const donationHandlingStateKey = {
  legacy: 'common.donationHandling.states.legacy',
  pending: 'common.donationHandling.states.pending',
  processed: 'common.donationHandling.states.processed',
  closed: 'common.donationHandling.states.closed',
} as const;

export const donationHandlingHelpKey = {
  legacy: 'common.donationHandling.help.legacy',
  pending: 'common.donationHandling.help.pending',
  processed: 'common.donationHandling.help.processed',
  closed: 'common.donationHandling.help.closed',
} as const;

export const donationHandlingRoleKey = {
  admin: 'common.donationHandling.roles.admin',
  steward: 'common.donationHandling.roles.steward',
} as const;

export const donationHandlingReasonKey = {
  rejected: 'common.donationHandling.reasons.rejected',
  withdrawn: 'common.donationHandling.reasons.withdrawn',
  terminated: 'common.donationHandling.reasons.terminated',
  expired: 'common.donationHandling.reasons.expired',
  member_removed: 'common.donationHandling.reasons.member_removed',
  account_deleted: 'common.donationHandling.reasons.account_deleted',
} as const;
