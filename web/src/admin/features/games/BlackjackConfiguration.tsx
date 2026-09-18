import { Card } from '@shared/components/States';
import type { BlackjackConfig } from '@shared/games/blackjack';
import { useGameAdminText } from './copy';
import { percentBP } from './config';

export function validateBlackjackConfiguration(
  config: { master_enabled: boolean; blackjack: BlackjackConfig },
  t: (zh: string, en: string) => string,
): string | null {
  const value = config.blackjack;
  if (value.enabled && !config.master_enabled)
    return t('请先开启小游戏总开关。', 'Enable the games master switch first.');
  const parse = (v: string) => {
    const m = /^(0|[1-9][0-9]*)(?:\.([0-9]{1,3}))?$/.exec(v.trim());
    return m ? BigInt(m[1]) * 1000n + BigInt((m[2] ?? '').padEnd(3, '0')) : null;
  };
  const min = parse(value.min_stake),
    max = parse(value.max_stake),
    step = parse(value.stake_step),
    initial = parse(value.default_stake);
  if (
    min === null ||
    max === null ||
    step === null ||
    initial === null ||
    min < 1n ||
    min > max ||
    max > 140625000000000n ||
    step < 1n ||
    step > 140625000000000n ||
    initial < min ||
    initial > max ||
    (max - min) % step !== 0n ||
    (initial - min) % step !== 0n
  )
    return t(
      '二十一点：金额须为正数且最多三位小数；最大投入不超过140625000000，默认值和最大值须符合步长。',
      'Blackjack: positive amounts with at most three decimals; maximum stake 140625000000. Default and maximum stakes must align with the step from the minimum.',
    );
  const rates = Object.values(value.rake_bp);
  if (
    rates.some((r) => !Number.isInteger(r) || r < 0 || r > 9999) ||
    rates.reduce((a, b) => a + b, 0) >= 10000
  )
    return t(
      '二十一点：每项费用为0至99.99%，三项合计小于100%。',
      'Blackjack: each fee must be 0–99.99%, and total fees must be below 100%.',
    );
  return null;
}
export function BlackjackConfiguration({
  value,
  disabled,
  onChange,
}: {
  readonly value: BlackjackConfig;
  readonly disabled: boolean;
  readonly onChange: (value: BlackjackConfig) => void;
}) {
  const t = useGameAdminText();
  return (
    <Card>
      <h2>{t('二十一点', 'Blackjack')}</h2>
      <p>
        {t(
          '单桌九席，每分钟按15秒落座、30秒决策、15秒展示轮转。修改配置不改变已经入队的投入及费用。关闭后候补和未发牌席位原退，已发牌局正常结算。',
          'One table with nine seats: 15 seconds for seating, 30 for decisions and 15 for results. Queued entries keep their stake and fees. Closing refunds waiters and undealt seats; dealt tables settle normally.',
        )}
      </p>
      <label className="checkbox-label">
        <input
          type="checkbox"
          checked={value.enabled}
          disabled={disabled}
          onChange={(e) => onChange({ ...value, enabled: e.target.checked })}
        />
        {t('开启二十一点', 'Enable Blackjack')}
      </label>
      <div className="ops-field-grid">
        {(['min_stake', 'max_stake', 'stake_step', 'default_stake'] as const).map((field) => (
          <label key={field}>
            <span>
              {
                {
                  min_stake: t('最小基础投入', 'Minimum base stake'),
                  max_stake: t('最大基础投入', 'Maximum base stake'),
                  stake_step: t('投入步长', 'Stake step'),
                  default_stake: t('默认基础投入', 'Default base stake'),
                }[field]
              }
            </span>
            <input
              type="text"
              inputMode="decimal"
              maxLength={20}
              value={value[field]}
              disabled={disabled}
              onChange={(e) => onChange({ ...value, [field]: e.target.value })}
            />
          </label>
        ))}
        {(['platform', 'welfare', 'thursday'] as const).map((field) => (
          <label key={field}>
            <span>
              {
                {
                  platform: t('平台费用（%）', 'Platform fee (%)'),
                  welfare: t('低保池费用（%）', 'Welfare pool fee (%)'),
                  thursday: t('周四池费用（%）', 'Thursday pool fee (%)'),
                }[field]
              }
            </span>
            <input
              type="number"
              min="0"
              max="99.99"
              step="0.01"
              value={Number.isNaN(value.rake_bp[field]) ? '' : value.rake_bp[field] / 100}
              disabled={disabled}
              onChange={(e) =>
                onChange({
                  ...value,
                  rake_bp: { ...value.rake_bp, [field]: percentBP(e.target.value) },
                })
              }
            />
          </label>
        ))}
      </div>
      <p className="table-note">
        {t(
          '费用逐手从应返总额扣取；正常返还全部为通用积分，包含平局和本金。服务器重启取消则按实际原币种退款。',
          'Fees apply to each hand’s gross return. Normal returns are all general credits, including pushes and principal. Restart cancellations refund the original payment assets.',
        )}
      </p>
    </Card>
  );
}
