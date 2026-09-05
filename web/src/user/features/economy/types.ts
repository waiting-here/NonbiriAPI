export type CharityCapabilityState =
  'feature_disabled' | 'no_models' | 'no_candidates' | 'available';

export interface CharityCapabilityTokenPrices {
  uncachedInput: string;
  cacheWriteInput: string;
  cacheReadInput: string;
  output: string;
}

export type CharityCapabilityPricing =
  | {
      mode: 'per_request';
      userPriceMilli: string;
      discountedUserPriceMilli: string;
    }
  | {
      mode: 'per_token';
      userPricesMilli: CharityCapabilityTokenPrices;
      discountedUserPricesMilli: CharityCapabilityTokenPrices;
    };

export interface CharityCapabilityDiscount {
  enabled: boolean;
  percent: number;
  startAt: number | null;
  endAt: number | null;
}

export interface CharityCapabilityModel {
  id: string;
  provider: string;
  model: string;
  fullName: string;
  pricing: CharityCapabilityPricing;
  discount: CharityCapabilityDiscount;
}

export interface CharityCapability {
  state: CharityCapabilityState;
  models: CharityCapabilityModel[];
  donationIntake: DonationIntakeState;
  serverNow: number;
}

export type DonationIntakeState = 'open' | 'closed';

export type DonationStatus = 'pending' | 'approved' | 'rejected' | 'deleted' | 'expired';

export type DonationKeyState =
  'pending' | 'disabled' | 'suspended' | 'exhausted' | 'expired' | 'ended' | 'available';

export type DonationKeyEndedReason =
  'member_removed' | 'withdrawn' | 'terminated' | 'expired' | 'account_deleted';

export interface DonationLimits {
  price: string | null;
  calls: string | null;
  tokens: string | null;
}

export interface DonationUsage {
  priceUsed: string;
  priceInflight: string;
  callsUsed: string;
  callsInflight: string;
  tokensUsed: string;
  tokensInflight: string;
}

export interface DonationStreak {
  generation: string;
  count: string;
  failureDisabled: boolean;
}

export interface DonationCustomSource {
  kind: 'custom';
  connectorType: string;
  baseUrl: string;
}

export interface DonationMainstreamSource {
  kind: 'mainstream';
  channelId: string;
  name: string;
  connectorType: string;
  baseUrl: string;
}

export type DonationKeySource = DonationCustomSource | DonationMainstreamSource;

export interface EndpointOriginCustom {
  kind: 'custom';
}

export interface EndpointOriginMainstream {
  kind: 'mainstream';
  channelId: string;
  name: string;
}

export type EndpointOrigin = EndpointOriginCustom | EndpointOriginMainstream;

export interface DonationKey {
  id: string;
  endpointKeyId: string | null;
  displayHead: string;
  displayTail: string;
  source: DonationKeySource;
  physicalEnabled: boolean;
  charityState: DonationKeyState;
  limits: DonationLimits;
  usage: DonationUsage;
  tokenReserve: number;
  streak: DonationStreak;
  expiresAt: number | null;
  endedReason: DonationKeyEndedReason | null;
}

export interface DonationReviewResult {
  decision: 'approve' | 'reject';
  reason: string;
  reviewedAt: number;
}

export interface Donation {
  id: string;
  status: DonationStatus;
  revision: string;
  description: string;
  reviewResult: DonationReviewResult | null;
  keys: DonationKey[];
  createdAt: number;
  updatedAt: number;
}

export interface CursorPage<T> {
  data: T[];
  nextCursor: string | null;
}

export interface EndpointSummary {
  id: string;
  connectorType: string;
  baseUrl: string;
  origin: EndpointOrigin;
  note: string;
  enabled: boolean;
  revision: string;
  keyCount: string;
  createdAt: number;
  updatedAt: number;
}

export interface EndpointKeySummary {
  id: string;
  endpointId: string;
  displayHead: string;
  displayTail: string;
  note: string;
  enabled: boolean;
  forceStoreFalse: boolean;
  maxConcurrency: number;
  maxRPM: number;
  suspensionState: 'none' | 'security_processing';
  revision: string;
  createdAt: number;
  updatedAt: number;
}

export type EndpointKeyEligibility = 'eligible' | 'already_donated' | 'security_processing';

export interface EndpointKeyChoice {
  endpoint: EndpointSummary;
  key: EndpointKeySummary;
  eligibility: EndpointKeyEligibility;
}

export type ActivitiesMasterReason = 'available' | 'disabled' | 'configuration_error';

export interface ActivitiesMaster {
  enabled: boolean;
  available: boolean;
  reason: ActivitiesMasterReason;
}

export type WelfareState =
  'unavailable' | 'available' | 'claimed' | 'ineligible' | 'empty' | 'configuration_error';

export interface WelfareView {
  enabled: boolean;
  state: WelfareState;
  siteDay: string;
  threshold: string;
  cap: string;
  poolBalance: string;
  claimedToday: boolean;
}

export type ThursdayState =
  'unavailable' | 'not_open' | 'open' | 'settling' | 'ended' | 'configuration_error';

export interface ThursdayCurrent {
  periodId: string;
  revision: string;
  opensAt: number;
  closesAt: number;
  literature: string;
  entry: string;
  perUserLimit: number;
  poolBalance: string;
  myCount: string;
  myContributed: string;
}

export interface ThursdayNext {
  periodId: string;
  opensAt: number;
  poolBalance: string;
}

export type ThursdayUnpaidReason = 'account_banned' | 'account_deleted';

export interface ThursdayLastResult {
  periodId: string;
  myCount: string;
  myContributed: string;
  payout: string;
  unpaidReason: ThursdayUnpaidReason | null;
}

export interface ThursdayView {
  enabled: boolean;
  state: ThursdayState;
  serverNow: number;
  current: ThursdayCurrent | null;
  next: ThursdayNext | null;
  lastResult: ThursdayLastResult | null;
}

export interface ActivitiesSnapshot {
  master: ActivitiesMaster;
  welfare: WelfareView;
  thursday: ThursdayView;
}

export interface WelfareClaimResult {
  awarded: string;
  balance: string;
  poolBalance: string;
  siteDay: string;
}

export interface ThursdayContributionResult {
  count: string;
  balance: string;
  poolBalance: string;
}

export type MutationFeedbackKind = 'success' | 'rejected' | 'unknown' | 'reconciled';

export interface MutationFeedback {
  kind: MutationFeedbackKind;
  action: 'create' | 'edit' | 'withdraw' | 'terminate' | 'welfare' | 'thursday';
  error?: unknown;
  awarded?: string;
}
