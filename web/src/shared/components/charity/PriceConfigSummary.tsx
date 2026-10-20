import { weekdayKeys } from '@shared/operations/recurringDiscount';
import { useTranslation } from 'react-i18next';
import type { CharityPriceConfig, CharityRole, TokenPrices } from '@shared/operations/charity';
import { Amount } from '@shared/components/Amount';
import { creditsToMilli } from '../../../user/games/common/strict';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { formatCount } from '@shared/utils/formatNumber';
import { charityCopyKey, tokenPriceCopyKey } from './managementFields';

export function PriceConfigBrief({
  config,
  role,
}: {
  config: CharityPriceConfig;
  role: CharityRole;
}) {
  const { t } = useTranslation();
  const pricing = config.pricing;
  const value = (price: string) => (
    <span className="nb-num">
      <Amount value={creditsToMilli(price).toString()} />
    </span>
  );
  return (
    <div className="nb-workflow-price-brief">
      <span>
        {t(charityCopyKey(role, pricing.mode === 'per_request' ? 'perRequest' : 'perToken'))}
      </span>
      {pricing.mode === 'per_request' ? (
        <span>
          {value(pricing.user_price)} / {value(pricing.donor_reward)}
        </span>
      ) : (
        <>
          <span>
            {t('common.workflow.inputPrice')} {value(pricing.user_prices.uncached_input)}
          </span>
          <span>
            {t('common.workflow.outputPrice')} {value(pricing.user_prices.output)}
          </span>
        </>
      )}
    </div>
  );
}

export function PriceConfigSummary({
  config,
  role,
}: {
  config: CharityPriceConfig;
  role: CharityRole;
}) {
  const { t } = useTranslation();
  const time = useDateTimeFormatter();
  const money = (value: string) => (
    <span className="nb-num">
      <Amount value={creditsToMilli(value).toString()} />
    </span>
  );
  const prices = (side: 'userPrices' | 'donorRewards', values: TokenPrices) =>
    (Object.keys(values) as (keyof TokenPrices)[]).map((field) => (
      <div key={`${side}:${field}`}>
        <dt>{t(tokenPriceCopyKey(role, side, field))}</dt>
        <dd>{money(values[field])}</dd>
      </div>
    ));
  const pricing = config.pricing;
  return (
    <dl className="nb-kv nb-workflow-prices">
      <div>
        <dt>{t('common.operations.charity.pricing')}</dt>
        <dd>
          {t(charityCopyKey(role, pricing.mode === 'per_request' ? 'perRequest' : 'perToken'))}
        </dd>
      </div>
      {pricing.mode === 'per_request' ? (
        <>
          <div>
            <dt>{t(charityCopyKey(role, 'request_user_price_milli'))}</dt>
            <dd>{money(pricing.user_price)}</dd>
          </div>
          <div>
            <dt>{t(charityCopyKey(role, 'request_donor_reward_milli'))}</dt>
            <dd>{money(pricing.donor_reward)}</dd>
          </div>
        </>
      ) : (
        <>
          {prices('userPrices', pricing.user_prices)}
          {prices('donorRewards', pricing.donor_rewards)}
        </>
      )}
      <div>
        <dt>{t('common.operations.charity.tokenReserveCredits')}</dt>
        <dd>
          {config.token_reserve_credits === null
            ? t('common.workflow.inherit')
            : money(config.token_reserve_credits)}
        </dd>
      </div>
      <div>
        <dt>{t('common.operations.charity.context.longThreshold')}</dt>
        <dd>
          {pricing.long_context ? (
            <span className="nb-num">
              {formatCount(pricing.long_context.input_threshold).display}
            </span>
          ) : (
            t('common.disabled')
          )}
        </dd>
      </div>
      {pricing.long_context?.user_price ? (
        <div>
          <dt>{t('common.operations.charity.context.requestPrice')}</dt>
          <dd>{money(pricing.long_context.user_price)}</dd>
        </div>
      ) : null}
      {pricing.long_context?.user_prices ? (
        <div>
          <dt>{t('common.operations.charity.context.long')}</dt>
          <dd>
            <dl>{prices('userPrices', pricing.long_context.user_prices)}</dl>
          </dd>
        </div>
      ) : null}
      <div>
        <dt>{t('common.operations.charity.context.penalty')}</dt>
        <dd>
          {pricing.context_penalty ? (
            <>
              <span className="nb-num">
                {formatCount(pricing.context_penalty.input_threshold).display}
              </span>{' '}
              · {money(pricing.context_penalty.fine)}
            </>
          ) : (
            t('common.disabled')
          )}
        </dd>
      </div>
      <div>
        <dt>{t('common.recurringDiscount.fixedTitle')}</dt>
        <dd>
          {t(config.discount.enabled ? 'common.enabled' : 'common.disabled')} ·{' '}
          <span className="nb-num">{config.discount.percent}%</span>
        </dd>
      </div>
      <div>
        <dt>{t('common.workflow.discountStart')}</dt>
        <dd>{config.discount.start_at === null ? '—' : time(config.discount.start_at)}</dd>
      </div>
      <div>
        <dt>{t('common.workflow.discountEnd')}</dt>
        <dd>{config.discount.end_at === null ? '—' : time(config.discount.end_at)}</dd>
      </div>
      <div>
        <dt>{t('common.recurringDiscount.title')}</dt>
        <dd>
          {t(config.recurring_discount.enabled ? 'common.enabled' : 'common.disabled')}
          <p>
            {t('common.recurringDiscount.zoneSummary', {
              zone: config.recurring_discount.time_zone,
            })}
          </p>
          <p>
            {t('common.recurringDiscount.defaultSummary', {
              percent: config.recurring_discount.default_percent,
            })}
          </p>
          {config.discount.enabled && config.recurring_discount.enabled ? (
            <p>{t('common.recurringDiscount.paused')}</p>
          ) : null}
          {config.recurring_discount.periods.map((period, index) => (
            <p key={index}>
              {t('common.recurringDiscount.periodSummary', {
                days: period.weekdays.map((day) => t(weekdayKeys[day - 1]!)).join(', '),
                start: period.start_time,
                end: period.end_time,
                percent: period.percent,
              })}
            </p>
          ))}
        </dd>
      </div>
    </dl>
  );
}
