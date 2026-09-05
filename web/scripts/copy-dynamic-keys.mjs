/**
 * Exact dynamic catalog leaves used by production template/helper composition.
 *
 * Every key is listed as a concrete leaf. The checker validates the expanded
 * entries against the bilingual catalogs, a live production-source anchor,
 * and a non-empty reason; prefixes and wildcards are not accepted.
 */
const entry = (catalog, key, source, anchor, reason) => ({
  catalog,
  key,
  source,
  anchor,
  reason,
});

const charityManagement = 'src/shared/components/CharityManagement.tsx';
const charityPanels = 'src/user/features/economy/CharityPanels.tsx';
const activitiesPanels = 'src/user/features/economy/ActivitiesPanels.tsx';

function anchorFor(key, defaultAnchor) {
  if (key.includes('.capabilityBody.'))
    return 't(`user.charity.capabilityBody.${capability.state}`)';
  if (key.includes('.intakeBody.')) return 't(`user.charity.intakeBody.${state}`)';
  if (key.endsWith('.withdraw') || key.endsWith('.terminate')) {
    return "t(`user.charity.${confirmation === 'withdraw' ? 'withdraw' : 'terminate'}`)";
  }
  if (key.endsWith('.withdrawTitle') || key.endsWith('.terminateTitle')) {
    return "user.charity.${confirmation === 'withdraw' ? 'withdrawTitle' : 'terminateTitle'}";
  }
  if (key.endsWith('.withdrawBody') || key.endsWith('.terminateBody')) {
    return "user.charity.${confirmation === 'withdraw' ? 'withdrawBody' : 'terminateBody'}";
  }
  if (key.includes('.masterBody.'))
    return 't(`user.activities.masterBody.${snapshot.master.reason}`)';
  if (key.includes('.welfare.body.')) return 't(`user.activities.welfare.body.${welfare.state}`)';
  if (key.includes('.thursday.body.'))
    return 't(`user.activities.thursday.body.${thursday.state}`)';
  if (key.includes('.thursday.unpaidReason.'))
    return 't(`user.activities.thursday.unpaidReason.${result.unpaidReason}`)';
  return defaultAnchor;
}

const dynamicCopyKeys = [
  // Common charity state/decision/validation domains are finite typed unions.
  ...[
    'common.operations.charity.charityState.pending',
    'common.operations.charity.charityState.available',
    'common.operations.charity.charityState.disabled',
    'common.operations.charity.charityState.suspended',
    'common.operations.charity.charityState.exhausted',
    'common.operations.charity.charityState.expired',
    'common.operations.charity.charityState.ended',
  ].map((key) =>
    entry(
      'common',
      key,
      charityManagement,
      't(charityStateKey(item.charity_state))',
      'The helper receives the finite CharityState union; this leaf is one explicitly reviewed output.',
    ),
  ),
  // Common charity review decisions are finite.
  ...[
    'common.operations.charity.decision.approve',
    'common.operations.charity.decision.reject',
  ].map((key) =>
    entry(
      'common',
      key,
      charityManagement,
      't(`common.operations.charity.decision.${item.review_result.decision}`)',
      'The decision value is the closed approve/reject review union.',
    ),
  ),
  // Common charity reviewer roles are finite.
  ...['common.operations.charity.role.admin', 'common.operations.charity.role.steward'].map((key) =>
    entry(
      'common',
      key,
      charityManagement,
      't(reviewerRoleKey(item.reviewer.role))',
      'Reviewer role is constrained to the admin/steward union before translation.',
    ),
  ),
  // Common charity source types are finite.
  ...[
    'common.operations.charity.sourceType.automatic',
    'common.operations.charity.sourceType.manual',
  ].map((key) =>
    entry(
      'common',
      key,
      'src/shared/components/CharityBindingPicker.tsx',
      't(sourceTypeKey(source))',
      'Source type is constrained to the automatic/manual union.',
    ),
  ),
  // Common charity validation messages are a finite validator-code union.
  ...[
    'common.operations.charity.validation.priceLimit',
    'common.operations.charity.validation.countLimits',
    'common.operations.charity.validation.tokenReserve',
    'common.operations.charity.validation.safeNote',
    'common.operations.charity.validation.reviewReason',
    'common.operations.charity.validation.expiry',
    'common.operations.charity.validation.expiryAuthorization',
    'common.operations.charity.validation.completeSettings',
    'common.operations.charity.validation.modelIdentity',
    'common.operations.charity.validation.modelPrices',
    'common.operations.charity.validation.discountPercent',
    'common.operations.charity.validation.discountDates',
  ].map((key) =>
    entry(
      'common',
      key,
      charityManagement,
      't(`common.operations.charity.validation.${validationError}`)',
      'The validator exposes this exact finite error code; no prefix or wildcard is registered.',
    ),
  ),
  // Common donation-key terminal reasons are mapped from a closed wire union.
  ...[
    'common.operations.charity.endedReasonValue.withdrawn',
    'common.operations.charity.endedReasonValue.terminated',
    'common.operations.charity.endedReasonValue.expired',
    'common.operations.charity.endedReasonValue.memberRemoved',
    'common.operations.charity.endedReasonValue.accountDeleted',
  ].map((key) =>
    entry(
      'common',
      key,
      charityManagement,
      '`common.operations.charity.endedReasonValue.${endedReasonKey[item.ended_reason]}`',
      'The ended-reason lookup maps the five-value DonationEndedReason union to this exact leaf.',
    ),
  ),
  // Common charity pricing side labels are selected from a two-value tuple.
  ...['common.operations.charity.userPrices', 'common.operations.charity.donorRewards'].map((key) =>
    entry(
      'common',
      key,
      charityManagement,
      "`common.operations.charity.${side === 'userPrices' ? 'userPrices' : 'donorRewards'}`",
      'The side expression has exactly the userPrices/donorRewards alternatives.',
    ),
  ),

  // Shared charity management role copy is an explicitly finite role/field set.
  ...[
    'admin.charity.actions',
    'admin.charity.allStatuses',
    'admin.charity.bindings',
    'admin.charity.deleteModelTitle',
    'admin.charity.disabled',
    'admin.charity.discountEnabled',
    'admin.charity.discountEnd',
    'admin.charity.discountPercent',
    'admin.charity.discountStart',
    'admin.charity.donationNumber',
    'admin.charity.donationsTitle',
    'admin.charity.flattenExperimental',
    'admin.charity.model',
    'admin.charity.modelsTitle',
    'admin.charity.newModel',
    'admin.charity.next',
    'admin.charity.noBindings',
    'admin.charity.noDonations',
    'admin.charity.noDonationsBody',
    'admin.charity.noModels',
    'admin.charity.noModelsBody',
    'admin.charity.order',
    'admin.charity.perRequest',
    'admin.charity.perToken',
    'admin.charity.previous',
    'admin.charity.pricingMode',
    'admin.charity.provider',
    'admin.charity.request_donor_reward_milli',
    'admin.charity.request_user_price_milli',
    'admin.charity.saveKeyLimits',
    'admin.charity.statusFilter',
    'admin.charity.successRate',
    'admin.charity.upstreamModel',
    'admin.charity.enabled',
  ].map((key) =>
    entry(
      'admin',
      key,
      charityManagement,
      key.endsWith('.donationsTitle') || key.endsWith('.modelsTitle')
        ? `charityCopyKey(frame, '${key.split('.').at(-1)}')`
        : key.endsWith('.enabled') || key.endsWith('.disabled')
          ? "charityCopyKey(role, model.enabled ? 'enabled' : 'disabled')"
          : key.endsWith('.approve') || key.endsWith('.reject')
            ? "charityCopyKey(role, decision === 'approve' ? 'approve' : 'reject')"
            : `charityCopyKey(role, '${key.split('.').at(-1)}')`,
      'charityCopyKey is constrained to admin/steward callers and this exact field is passed to it.',
    ),
  ),
  ...[
    'user.steward.actions',
    'user.steward.allStatuses',
    'user.steward.bindings',
    'user.steward.deleteModelTitle',
    'user.steward.disabled',
    'user.steward.discountEnabled',
    'user.steward.discountEnd',
    'user.steward.discountPercent',
    'user.steward.discountStart',
    'user.steward.donationNumber',
    'user.steward.donationsTitle',
    'user.steward.flattenExperimental',
    'user.steward.model',
    'user.steward.modelsTitle',
    'user.steward.newModel',
    'user.steward.next',
    'user.steward.noBindings',
    'user.steward.noDonations',
    'user.steward.noDonationsBody',
    'user.steward.noModels',
    'user.steward.noModelsBody',
    'user.steward.order',
    'user.steward.perRequest',
    'user.steward.perToken',
    'user.steward.previous',
    'user.steward.pricingMode',
    'user.steward.provider',
    'user.steward.request_donor_reward_milli',
    'user.steward.request_user_price_milli',
    'user.steward.saveKeyLimits',
    'user.steward.statusFilter',
    'user.steward.successRate',
    'user.steward.upstreamModel',
    'user.steward.enabled',
  ].map((key) =>
    entry(
      'user',
      key,
      charityManagement,
      key.endsWith('.donationsTitle') || key.endsWith('.modelsTitle')
        ? `charityCopyKey(frame, '${key.split('.').at(-1)}')`
        : key.endsWith('.enabled') || key.endsWith('.disabled')
          ? "charityCopyKey(role, model.enabled ? 'enabled' : 'disabled')"
          : key.endsWith('.approve') || key.endsWith('.reject')
            ? "charityCopyKey(role, decision === 'approve' ? 'approve' : 'reject')"
            : `charityCopyKey(role, '${key.split('.').at(-1)}')`,
      'charityCopyKey is constrained to admin/steward callers and this exact field is passed to it.',
    ),
  ),
  // Shared charity management decisions are selected from a closed pair.
  ...['admin.charity.approve', 'admin.charity.reject'].map((key) =>
    entry(
      'admin',
      key,
      charityManagement,
      "charityCopyKey(role, decision === 'approve' ? 'approve' : 'reject')",
      'The review action is constrained to the explicit approve/reject pair for the admin role.',
    ),
  ),
  ...['user.steward.approve', 'user.steward.reject'].map((key) =>
    entry(
      'user',
      key,
      charityManagement,
      "charityCopyKey(role, decision === 'approve' ? 'approve' : 'reject')",
      'The review action is constrained to the explicit approve/reject pair for the steward role.',
    ),
  ),
  // Shared charity management statuses use the closed donation-status union.
  ...[
    'admin.charity.status.pending',
    'admin.charity.status.approved',
    'admin.charity.status.rejected',
    'admin.charity.status.deleted',
    'admin.charity.status.expired',
  ].map((key) =>
    entry(
      'admin',
      key,
      charityManagement,
      't(charityStatusKey(role, item.status))',
      'charityStatusKey receives the finite pending/approved/rejected/deleted/expired status union.',
    ),
  ),
  ...[
    'user.steward.status.pending',
    'user.steward.status.approved',
    'user.steward.status.rejected',
    'user.steward.status.deleted',
    'user.steward.status.expired',
  ].map((key) =>
    entry(
      'user',
      key,
      charityManagement,
      't(charityStatusKey(role, item.status))',
      'charityStatusKey receives the finite pending/approved/rejected/deleted/expired status union.',
    ),
  ),
  // Shared charity management token-price labels use the closed field/side product.
  ...[
    'admin.charity.uncached_user_price_milli',
    'admin.charity.cache_write_user_price_milli',
    'admin.charity.cache_read_user_price_milli',
    'admin.charity.output_user_price_milli',
    'admin.charity.uncached_donor_reward_milli',
    'admin.charity.cache_write_donor_reward_milli',
    'admin.charity.cache_read_donor_reward_milli',
    'admin.charity.output_donor_reward_milli',
  ].map((key) =>
    entry(
      'admin',
      key,
      charityManagement,
      't(tokenPriceCopyKey(role, side, field))',
      'tokenPriceCopyKey combines four token fields with the two explicit pricing sides.',
    ),
  ),
  ...[
    'user.steward.uncached_user_price_milli',
    'user.steward.cache_write_user_price_milli',
    'user.steward.cache_read_user_price_milli',
    'user.steward.output_user_price_milli',
    'user.steward.uncached_donor_reward_milli',
    'user.steward.cache_write_donor_reward_milli',
    'user.steward.cache_read_donor_reward_milli',
    'user.steward.output_donor_reward_milli',
  ].map((key) =>
    entry(
      'user',
      key,
      charityManagement,
      't(tokenPriceCopyKey(role, side, field))',
      'tokenPriceCopyKey combines four token fields with the two explicit pricing sides.',
    ),
  ),

  // User charity capability state/body domains are finite normalized unions.
  ...[
    'user.charity.capabilityState.feature_disabled',
    'user.charity.capabilityState.no_models',
    'user.charity.capabilityState.no_candidates',
    'user.charity.capabilityState.available',
    'user.charity.capabilityBody.feature_disabled',
    'user.charity.capabilityBody.no_models',
    'user.charity.capabilityBody.no_candidates',
    'user.charity.capabilityBody.available',
  ].map((key) =>
    entry(
      'user',
      key,
      charityPanels,
      anchorFor(key, 't(`user.charity.capabilityState.${capability.state}`)'),
      'The normalized capability state is a four-value closed union.',
    ),
  ),
  // User charity intake state/body domains are finite.
  ...[
    'user.charity.intakeState.open',
    'user.charity.intakeState.closed',
    'user.charity.intakeBody.open',
    'user.charity.intakeBody.closed',
  ].map((key) =>
    entry(
      'user',
      key,
      charityPanels,
      anchorFor(key, 't(`user.charity.intakeState.${state}`)'),
      'The normalized intake state is the explicit open/closed union.',
    ),
  ),
  // User charity key-eligibility results are finite.
  ...[
    'user.charity.keyEligibility.eligible',
    'user.charity.keyEligibility.already_donated',
    'user.charity.keyEligibility.security_processing',
  ].map((key) =>
    entry(
      'user',
      key,
      charityPanels,
      't(`user.charity.keyEligibility.${choice.eligibility}`)',
      'Eligibility is normalized to the explicit eligible/already_donated/security_processing union.',
    ),
  ),
  // User charity donation status labels use the finite status union.
  ...[
    'user.charity.status.pending',
    'user.charity.status.approved',
    'user.charity.status.rejected',
    'user.charity.status.deleted',
    'user.charity.status.expired',
  ].map((key) =>
    entry(
      'user',
      key,
      charityPanels,
      't(`user.charity.status.${donationStatus}`)',
      'Donation status is normalized to the five-value pending/approved/rejected/deleted/expired union.',
    ),
  ),
  // User charity key state labels are finite outputs of keyStateKeys.
  ...[
    'user.charity.keyState.pending',
    'user.charity.keyState.disabled',
    'user.charity.keyState.physical_disabled',
    'user.charity.keyState.suspended',
    'user.charity.keyState.failure_disabled',
    'user.charity.keyState.charity_paused',
    'user.charity.keyState.price_exhausted',
    'user.charity.keyState.calls_exhausted',
    'user.charity.keyState.tokens_exhausted',
    'user.charity.keyState.exhausted',
    'user.charity.keyState.expired',
    'user.charity.keyState.ended',
    'user.charity.keyState.available',
  ].map((key) =>
    entry(
      'user',
      key,
      charityPanels,
      't(`user.charity.keyState.${state}`)',
      'keyStateKeys emits only this explicitly reviewed set of terminal, blocking, quota, and available states.',
    ),
  ),
  // User charity ended-reason labels use the finite normalized union.
  ...[
    'user.charity.endedReasonValue.member_removed',
    'user.charity.endedReasonValue.withdrawn',
    'user.charity.endedReasonValue.terminated',
    'user.charity.endedReasonValue.expired',
    'user.charity.endedReasonValue.account_deleted',
  ].map((key) =>
    entry(
      'user',
      key,
      charityPanels,
      't(`user.charity.endedReasonValue.${donationKey.endedReason}`)',
      'Ended reason is normalized to the five-value member_removed/withdrawn/terminated/expired/account_deleted union.',
    ),
  ),
  // User charity confirmation copy is selected from a closed action pair.
  ...[
    'user.charity.withdraw',
    'user.charity.terminate',
    'user.charity.withdrawTitle',
    'user.charity.withdrawBody',
    'user.charity.terminateTitle',
    'user.charity.terminateBody',
  ].map((key) =>
    entry(
      'user',
      key,
      charityPanels,
      anchorFor(
        key,
        "user.charity.${confirmation === 'withdraw' ? 'withdrawTitle' : 'terminateTitle'}",
      ),
      'Confirmation is constrained to withdraw or terminate and selects this exact title/body/action leaf.',
    ),
  ),
  // Legal pages resolve the authoritative locale through a finite locale union.
  ...['user.legal.localeName.zh', 'user.legal.localeName.en'].map((key) =>
    entry(
      'user',
      key,
      'src/user/pages/PrivacyPage.tsx',
      't(`user.legal.localeName.${authoritative}`)',
      'The authoritative locale is constrained to the explicit zh/en pair.',
    ),
  ),

  // User activity master state/body domains are finite normalized states.
  ...[
    'user.activities.masterState.available',
    'user.activities.masterState.disabled',
    'user.activities.masterState.configuration_error',
    'user.activities.masterBody.available',
    'user.activities.masterBody.disabled',
    'user.activities.masterBody.configuration_error',
  ].map((key) =>
    entry(
      'user',
      key,
      activitiesPanels,
      anchorFor(key, 't(`user.activities.masterState.${snapshot.master.reason}`)'),
      'Master reason is normalized to available/disabled/configuration_error.',
    ),
  ),
  // User activity stream states are finite connection/recovery states.
  ...[
    'user.activities.stream.connecting',
    'user.activities.stream.connected',
    'user.activities.stream.disconnected',
    'user.activities.stream.reconciled',
    'user.activities.stream.recoveryFailed',
  ].map((key) =>
    entry(
      'user',
      key,
      activitiesPanels,
      't(`user.activities.stream.${key}`)',
      'The connection notice selects only the explicit connecting/connected/disconnected/reconciled/recoveryFailed set.',
    ),
  ),
  // User welfare state/body domains are finite.
  ...[
    'user.activities.welfare.state.unavailable',
    'user.activities.welfare.state.available',
    'user.activities.welfare.state.claimed',
    'user.activities.welfare.state.ineligible',
    'user.activities.welfare.state.empty',
    'user.activities.welfare.state.configuration_error',
    'user.activities.welfare.body.unavailable',
    'user.activities.welfare.body.available',
    'user.activities.welfare.body.claimed',
    'user.activities.welfare.body.ineligible',
    'user.activities.welfare.body.empty',
    'user.activities.welfare.body.configuration_error',
  ].map((key) =>
    entry(
      'user',
      key,
      activitiesPanels,
      anchorFor(key, 't(`user.activities.welfare.state.${welfare.state}`)'),
      'Welfare state is normalized to the six explicit unavailable/available/claimed/ineligible/empty/configuration_error values.',
    ),
  ),
  // Thursday state/body and unpaid-reason domains are finite.
  ...[
    'user.activities.thursday.state.unavailable',
    'user.activities.thursday.state.not_open',
    'user.activities.thursday.state.open',
    'user.activities.thursday.state.settling',
    'user.activities.thursday.state.ended',
    'user.activities.thursday.state.configuration_error',
    'user.activities.thursday.body.unavailable',
    'user.activities.thursday.body.not_open',
    'user.activities.thursday.body.open',
    'user.activities.thursday.body.settling',
    'user.activities.thursday.body.ended',
    'user.activities.thursday.body.configuration_error',
    'user.activities.thursday.unpaidReason.account_banned',
    'user.activities.thursday.unpaidReason.account_deleted',
  ].map((key) =>
    entry(
      'user',
      key,
      activitiesPanels,
      anchorFor(key, 't(`user.activities.thursday.state.${thursday.state}`)'),
      'Thursday state is normalized to the six explicit lifecycle values; unpaid reasons are a closed pair.',
    ),
  ),
];

export default Object.freeze(dynamicCopyKeys);
