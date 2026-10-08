import { usePictureBookText } from '@shared/picturebook/copy';
import { useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import '@shared/operations/operations.css';
import type { ImageModel } from '@shared/picturebook/publicTypes';
import type { PriceDraft } from './prices';

export function PriceFields({
  model,
  value,
  onChange,
}: {
  readonly model: Pick<ImageModel, 'size_capability'>;
  readonly value: PriceDraft;
  readonly onChange: (next: PriceDraft) => void;
}) {
  const t = usePictureBookText();
  const disclosure = useRef<HTMLDetailsElement>(null);
  const activePrices = value.tiers.length > 0 || value.sizes.length > 0;
  useEffect(() => {
    if (activePrices && disclosure.current) disclosure.current.open = true;
  }, [activePrices]);
  const { t: translate } = useTranslation();
  const tiers = [
    ...new Set([
      ...(model.size_capability?.combinations ?? []).flatMap((row) => (row.tier ? [row.tier] : [])),
      ...(model.size_capability?.auto ? ['auto'] : []),
      ...value.tiers.map((row) => row.tier),
    ]),
  ];
  const nextTier = tiers.find((tier) => !value.tiers.some((row) => row.tier === tier));
  const nextSize = model.size_capability?.combinations?.find(
    (row) =>
      row.width &&
      row.height &&
      !value.sizes.some(
        (price) => Number(price.width) === row.width && Number(price.height) === row.height,
      ),
  );
  const currency = (label: string, amount: string, update: (next: string) => void) => (
    <label>
      {label}
      <input
        inputMode="numeric"
        pattern="0|[1-9][0-9]*"
        maxLength={39}
        value={amount}
        required
        onChange={(event) => update(event.target.value)}
      />
    </label>
  );
  return (
    <>
      <div className="picturebook-grid">
        {currency(t('每张草稿纸价格', 'Sketch paper per image'), value.paper, (paper) =>
          onChange({ ...value, paper }),
        )}
        {currency(t('每张画笔价格', 'Brushes per image'), value.brush, (brush) =>
          onChange({ ...value, brush }),
        )}
      </div>
      <p className="picturebook-help">
        {t(
          '价格使用整数，两种活动币至少一种大于0。',
          'Prices are whole units; at least one activity currency must be greater than zero.',
        )}
      </p>
      <label>
        {t('未指定尺寸价格时', 'When no size price matches')}
        <select
          value={value.fallback}
          onChange={(event) =>
            onChange({
              ...value,
              fallback: event.target.value as PriceDraft['fallback'],
            })
          }
        >
          <option value="default">{t('使用默认价', 'Use default price')}</option>
          <option value="unavailable">{t('该尺寸不可提交', 'Make that size unavailable')}</option>
        </select>
      </label>
      <details className="ops-advanced" ref={disclosure}>
        <summary>
          <strong>{translate('common.operations.management.optionalPrices')}</strong>
          {' · '}
          {translate('common.operations.management.sizePricesSummary', {
            tiers: value.tiers.length,
            sizes: value.sizes.length,
          })}
        </summary>
        <section aria-label={t('尺寸档位价格', 'Size tier prices')} className="picturebook-stack">
          <h4>{t('尺寸档位价格', 'Size tier prices')}</h4>
          {value.tiers.map((row, index) => (
            <fieldset className="picturebook-card" key={index}>
              <legend>
                {t('档位价格', 'Tier price')} {index + 1}
              </legend>
              <div className="picturebook-price-row">
                <label>
                  {t('价格档位', 'Price tier')} {index + 1}
                  <select
                    value={row.tier}
                    onChange={(event) =>
                      onChange({
                        ...value,
                        tiers: value.tiers.map((entry, i) =>
                          i === index ? { ...entry, tier: event.target.value } : entry,
                        ),
                      })
                    }
                  >
                    {tiers.map((tier) => (
                      <option key={tier} value={tier}>
                        {tier}
                      </option>
                    ))}
                  </select>
                </label>
                {currency(
                  t('档位草稿纸', 'Tier sketch paper') + ' ' + (index + 1),
                  row.paper,
                  (paper) =>
                    onChange({
                      ...value,
                      tiers: value.tiers.map((entry, i) =>
                        i === index ? { ...entry, paper } : entry,
                      ),
                    }),
                )}
                {currency(t('档位画笔', 'Tier brushes') + ' ' + (index + 1), row.brush, (brush) =>
                  onChange({
                    ...value,
                    tiers: value.tiers.map((entry, i) =>
                      i === index ? { ...entry, brush } : entry,
                    ),
                  }),
                )}
              </div>
              <button
                className="nb-btn nb-btn--secondary"
                type="button"
                onClick={() =>
                  onChange({
                    ...value,
                    tiers: value.tiers.filter((_, i) => i !== index),
                  })
                }
              >
                {t('移除档位价格', 'Remove tier price')} {index + 1}
              </button>
            </fieldset>
          ))}
          <button
            className="nb-btn nb-btn--secondary"
            type="button"
            disabled={!nextTier || value.tiers.length >= 64}
            onClick={() =>
              nextTier &&
              onChange({
                ...value,
                tiers: [...value.tiers, { tier: nextTier, paper: value.paper, brush: value.brush }],
              })
            }
          >
            {t('添加档位价格', 'Add tier price')}
          </button>
        </section>
        <section aria-label={t('精确尺寸价格', 'Exact size prices')} className="picturebook-stack">
          <h4>{t('精确尺寸价格', 'Exact size prices')}</h4>
          {value.sizes.map((row, index) => (
            <fieldset className="picturebook-card" key={index}>
              <legend>
                {t('尺寸价格', 'Size price')} {index + 1}
              </legend>
              <div className="picturebook-price-row">
                {(['width', 'height'] as const).map((axis) => (
                  <label key={axis}>
                    {axis === 'width'
                      ? t('价格宽度', 'Price width')
                      : t('价格高度', 'Price height')}{' '}
                    {index + 1}
                    <input
                      type="number"
                      min={1}
                      max={65536}
                      step={1}
                      inputMode="numeric"
                      value={row[axis]}
                      required
                      onChange={(event) =>
                        onChange({
                          ...value,
                          sizes: value.sizes.map((entry, i) =>
                            i === index ? { ...entry, [axis]: event.target.value } : entry,
                          ),
                        })
                      }
                    />
                  </label>
                ))}
                {currency(
                  t('尺寸草稿纸', 'Size sketch paper') + ' ' + (index + 1),
                  row.paper,
                  (paper) =>
                    onChange({
                      ...value,
                      sizes: value.sizes.map((entry, i) =>
                        i === index ? { ...entry, paper } : entry,
                      ),
                    }),
                )}
                {currency(t('尺寸画笔', 'Size brushes') + ' ' + (index + 1), row.brush, (brush) =>
                  onChange({
                    ...value,
                    sizes: value.sizes.map((entry, i) =>
                      i === index ? { ...entry, brush } : entry,
                    ),
                  }),
                )}
              </div>
              <button
                className="nb-btn nb-btn--secondary"
                type="button"
                onClick={() =>
                  onChange({
                    ...value,
                    sizes: value.sizes.filter((_, i) => i !== index),
                  })
                }
              >
                {t('移除尺寸价格', 'Remove size price')} {index + 1}
              </button>
            </fieldset>
          ))}
          <button
            className="nb-btn nb-btn--secondary"
            type="button"
            disabled={value.sizes.length >= 2048}
            onClick={() =>
              onChange({
                ...value,
                sizes: [
                  ...value.sizes,
                  {
                    width: nextSize?.width ? String(nextSize.width) : '',
                    height: nextSize?.height ? String(nextSize.height) : '',
                    paper: value.paper,
                    brush: value.brush,
                  },
                ],
              })
            }
          >
            {t('添加尺寸价格', 'Add size price')}
          </button>
        </section>
      </details>
    </>
  );
}
