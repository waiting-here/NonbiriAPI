import { useTranslation } from 'react-i18next';
import { Fold } from '@shared/components/ui';
import type { ReactElement } from 'react';
import { formatCredits } from '../../user/games/common/strict';
import { formatCount } from '@shared/utils/formatNumber';
import { NATIVE_SERVICE_LABEL_KEYS, type NativeService } from '@shared/nativeServices';
import { AgentTime } from './AgentParts';
import type { OperationPreview, PlanOperation } from './api';

const operationNames: Record<string, string> = {
  'memory.edit': 'common.agent.operationNames.memory',
  'donation.review': 'common.agent.operationNames.review',
  'donation.key.update': 'common.agent.operationNames.key',
  'donation.key.failure_policy': 'common.agent.operationNames.failurePolicy',
  'donation.key.reset': 'common.agent.operationNames.resetKey',
  'donation.use.reset': 'common.agent.operationNames.resetUse',
  'donation.handle': 'common.agent.operationNames.handle',
  'users.profile': 'common.agent.operationNames.profile',
  'users.economy': 'common.agent.operationNames.economy',
  'users.ban': 'common.agent.operationNames.ban',
  'users.unban': 'common.agent.operationNames.unban',
  'users.permissions': 'common.agent.operationNames.permissions',
  'users.caller_key_revoke': 'common.agent.operationNames.callerKey',
  'users.mfa_reset': 'common.agent.operationNames.mfa',
  'users.delete': 'common.agent.operationNames.deleteUser',
  'charity.model.create': 'common.agent.operationNames.createModel',
  'charity.model.update': 'common.agent.operationNames.updateModel',
  'charity.model.delete': 'common.agent.operationNames.deleteModel',
  'charity.binding.add': 'common.agent.operationNames.addBinding',
  'charity.binding.update': 'common.agent.operationNames.updateBinding',
  'charity.binding.delete': 'common.agent.operationNames.deleteBinding',
  'charity.binding.apply_preset': 'common.agent.operationNames.applyPreset',
};
const fieldNames: Record<string, string> = {
  endpoint_limit: 'common.agent.changeFields.endpoints',
  rpm_limit: 'common.agent.changeFields.rpm',
  concurrency_limit: 'common.agent.changeFields.concurrency',
  light_rpm_limit: 'common.agent.changeFields.lightRPM',
  light_concurrency_limit: 'common.agent.changeFields.lightConcurrency',
  global_rpm_limit: 'common.agent.changeFields.globalRPM',
  level: 'common.agent.changeFields.level',
  lang: 'common.agent.changeFields.language',
  discord_gate_policy: 'common.agent.changeFields.discordGate',
  target: 'common.agent.changeFields.balance',
  direction: 'common.agent.changeFields.direction',
  amount: 'common.agent.changeFields.amount',
  reason: 'common.agent.changeFields.reason',
  duration_seconds: 'common.agent.changeFields.duration',
  donation_scope: 'management.permissions.scope.title',
  key_id: 'common.agent.changeFields.key',
  use_id: 'common.agent.changeFields.use',
  binding_id: 'common.agent.changeFields.binding',
  max_concurrency: 'common.agent.changeFields.concurrency',
  max_rpm: 'common.agent.changeFields.rpm',
  enabled: 'common.agent.changeFields.enabled',
  price_limit: 'common.agent.changeFields.creditLimit',
  calls_limit: 'common.agent.changeFields.calls',
  tokens_limit: 'common.agent.changeFields.tokens',
  token_reserve: 'common.agent.changeFields.tokenReserve',
  safe_note: 'common.agent.changeFields.note',
  expires_at: 'common.agent.changeFields.expires',
  threshold: 'common.agent.changeFields.failureThreshold',
  reset_failure_streak: 'common.agent.changeFields.resetFailures',
  provider: 'common.agent.changeFields.provider',
  model: 'common.agent.changeFields.model',
  model_types: 'common.agent.changeFields.apis',
  public_description: 'common.agent.changeFields.description',
  allowed_levels: 'common.agent.changeFields.allowedLevels',
  is_mainstream: 'common.agent.changeFields.mainstream',
  route_strategy: 'common.agent.changeFields.routing',
  pricing: 'common.agent.changeFields.pricing',
  mode: 'common.agent.changeFields.pricingMode',
  user_price: 'common.agent.changeFields.userPrice',
  donor_reward: 'common.agent.changeFields.donorReward',
  user_prices: 'common.agent.changeFields.userPrices',
  donor_rewards: 'common.agent.changeFields.donorRewards',
  uncached_input: 'common.agent.changeFields.input',
  cache_write_input: 'common.agent.changeFields.cacheWrite',
  cache_read_input: 'common.agent.changeFields.cacheRead',
  output: 'common.agent.changeFields.output',
  discount: 'common.agent.changeFields.discount',
  percent: 'common.agent.changeFields.percent',
  donation_key_id: 'common.agent.changeFields.key',
  upstream_model_id: 'common.agent.changeFields.upstreamModel',
  native_services: 'common.agent.changeFields.services',
};
const valueNames: Record<string, string> = {
  approve: 'common.agent.decisions.approve',
  reject: 'common.agent.decisions.reject',
  increase: 'common.agent.changeValues.increase',
  decrease: 'common.agent.changeValues.decrease',
  balance: 'common.agent.changeValues.balance',
  game_balance: 'common.agent.changeValues.gameBalance',
  donation_credit: 'common.agent.changeValues.donationCredit',
  zh: 'common.agent.changeValues.zh',
  en: 'common.agent.changeValues.en',
  per_request: 'common.agent.changeValues.perRequest',
  per_token: 'common.agent.changeValues.perToken',
  per_search_unit: 'common.agent.changeValues.perSearch',
};
function object(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}
function snapshot(value?: string) {
  try {
    return object(JSON.parse(value ?? '{}'));
  } catch {
    return {};
  }
}
export function OperationSummary({
  operation,
  preview,
}: {
  operation: PlanOperation;
  preview?: OperationPreview;
}) {
  const { t } = useTranslation();
  const input = object(operation.input);
  let before = snapshot(preview?.before);
  if (input.key_id && Array.isArray(before.keys)) {
    before = object(before.keys.find((key) => object(key).id === input.key_id));
  }
  const preset = object(before.preset);
  const mappings = Array.isArray(preset.mappings) ? preset.mappings : [];
  const renderValue = (key: string, value: unknown) => {
    if (value === null)
      return t(
        key === 'duration_seconds'
          ? 'common.agent.changeValues.permanent'
          : operation.kind === 'users.profile'
            ? 'common.agent.changeValues.default'
            : 'common.agent.changeValues.noLimit',
      );
    if (typeof value === 'boolean')
      return t(value ? 'common.agent.changeValues.enabled' : 'common.agent.changeValues.disabled');
    if (Array.isArray(value))
      return value
        .map((item) =>
          typeof item === 'string' && item in NATIVE_SERVICE_LABEL_KEYS
            ? t(NATIVE_SERVICE_LABEL_KEYS[item as NativeService])
            : String(item),
        )
        .join(', ');
    if (key === 'expires_at' && typeof value === 'number')
      return <AgentTime value={value * 1000} />;
    if (key === 'duration_seconds')
      return t('common.agent.changeValues.seconds', { value: formatCount(String(value)).display });
    const text = String(value);
    if (key.endsWith('_id')) return text;
    if (
      key === 'amount' ||
      key === 'price_limit' ||
      [
        'user_price',
        'donor_reward',
        'uncached_input',
        'cache_write_input',
        'cache_read_input',
        'output',
      ].includes(key)
    )
      return (
        <span className="agent-number">
          {formatCredits(text)} {t('common.creditsUnit')}
        </span>
      );
    return valueNames[text] ? (
      t(valueNames[text])
    ) : /^\d+$/.test(text) ? (
      <span className="agent-number">{formatCount(text).display}</span>
    ) : (
      text
    );
  };
  const fields = (
    value: Record<string, unknown>,
    old: Record<string, unknown>,
    prefix = '',
  ): ReactElement[] =>
    Object.entries(value).flatMap(([key, next]) => {
      const label = fieldNames[key];
      if (!label || JSON.stringify(next) === JSON.stringify(old[key])) return [];
      const name = prefix + t(label);
      if (next && typeof next === 'object' && !Array.isArray(next))
        return fields(object(next), object(old[key]), name + ' · ');
      const previous =
        key === 'level' && typeof old.level === 'object' ? object(old.level).manual : old[key];
      return [
        <p key={prefix + key}>
          <strong>{name}: </strong>
          {previous !== undefined ? <>{renderValue(key, previous)} → </> : null}
          {renderValue(key, next)}
        </p>,
      ];
    });
  const name =
    operation.kind === 'donation.review'
      ? input.decision === 'approve'
        ? 'common.agent.operationNames.approveReview'
        : input.decision === 'force_reject'
          ? 'common.agent.operationNames.forceReject'
          : 'common.agent.operationNames.rejectReview'
      : operationNames[operation.kind];
  return (
    <div className="nb-stack nb-stack--sm">
      <strong>{name ? t(name) : preview?.title || operation.kind}</strong>
      {operation.target_id ? (
        <p>
          {t(
            operation.kind.startsWith('users.')
              ? 'common.agent.targetUser'
              : operation.kind.startsWith('donation.') ||
                  operation.kind === 'charity.binding.apply_preset'
                ? 'common.agent.targetDonation'
                : operation.kind.startsWith('charity.')
                  ? 'common.agent.targetModel'
                  : 'common.agent.targetMemory',
            {
              id:
                operation.target_id === 'chat'
                  ? t('common.agent.chatMemory')
                  : operation.target_id === 'auto_review'
                    ? t('common.agent.reviewMemory')
                    : operation.target_id,
            },
          )}
        </p>
      ) : null}
      {fields(input, before)}
      {operation.kind === 'charity.binding.apply_preset' ? (
        <>
          <p>
            <strong>{t('common.agent.presetChannel')}: </strong>
            {String(object(before.channel).name ?? input.channel_id ?? '')}
          </p>
          {mappings.length ? (
            <Fold title={t('common.agent.presetMappings', { count: mappings.length })}>
              {mappings.map((value, index) => {
                const mapping = object(value);
                return (
                  <p key={index}>
                    {String(mapping.upstream_model_id ?? '')} →{' '}
                    {typeof mapping.full_name === 'string'
                      ? mapping.full_name
                      : t('common.agent.targetModel', {
                          id: String(mapping.charity_model_id ?? ''),
                        })}
                  </p>
                );
              })}
            </Fold>
          ) : null}
          <p className="nb-small nb-muted">{t('common.agent.presetSkips')}</p>
        </>
      ) : null}
      {Array.isArray(input.targets)
        ? input.targets.map((target, index) => (
            <div key={index}>{fields(object(target), {}, String(index + 1) + '. ')}</div>
          ))
        : null}
    </div>
  );
}
