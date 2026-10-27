import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Fold, Note } from '@shared/components/ui';
import { NativeServicesField } from '@shared/components/NativeServicesField';
import { ServiceRoutesSummary } from '@shared/components/ServiceRoutesSummary';
import type { NativeService } from '@shared/nativeServices';
import type { ServiceProjection } from '@shared/connections';
import type { BindingCandidate, BindingTargetInput } from '../types';
import { PersonalBindingPicker } from '../models/PersonalBindingPicker';
import { useBindingCopy } from '../models/bindingCopy';

export interface MatrixTarget
  extends BindingTargetInput, Partial<Omit<ServiceProjection, 'native_services'>> {
  label: string;
  options: NativeService[];
}
export function TargetMatrix({
  accountId,
  modelId,
  value,
  onChange,
  disabled = false,
  bound = new Set<string>(),
}: {
  accountId: string;
  modelId: string;
  value: MatrixTarget[];
  onChange: (value: MatrixTarget[]) => void;
  disabled?: boolean;
  bound?: ReadonlySet<string>;
}) {
  const { t } = useTranslation();
  const copy = useBindingCopy();
  const [bulk, setBulk] = useState<NativeService[]>([]);
  const options = [...new Set(value.flatMap((row) => row.options))];
  const identity = (row: Pick<BindingTargetInput, 'endpoint_key_id' | 'upstream_model_id'>) =>
    row.endpoint_key_id + '\u0000' + row.upstream_model_id;
  const add = (candidate: BindingCandidate) =>
    onChange([
      ...value,
      {
        endpoint_key_id: candidate.endpoint_key_id,
        upstream_model_id: candidate.upstream_model_id,
        native_services: [...(candidate.native_services ?? [])],
        options: candidate.native_services ?? [],
        label: candidate.endpoint_note || candidate.endpoint_base_url,
        service_routes: candidate.service_routes,
        capabilities: candidate.capabilities,
      },
    ]);
  const change = (index: number, patch: Partial<MatrixTarget>) =>
    onChange(value.map((row, i) => (i === index ? { ...row, ...patch } : row)));
  const move = (index: number, offset: number) => {
    const rows = value.slice();
    const other = index + offset;
    if (other < 0 || other >= rows.length) return;
    [rows[index], rows[other]] = [rows[other]!, rows[index]!];
    onChange(rows);
  };
  return (
    <div className="nb-stack">
      <Note>{copy.combinationOrder}</Note>
      {value.length > 1 ? (
        <Fold title={t('common.connections.bulkNativeServices')}>
          <NativeServicesField
            options={options}
            value={bulk}
            onChange={setBulk}
            disabled={disabled}
          />
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            disabled={disabled || !bulk.length}
            onClick={() =>
              onChange(
                value.map((row) =>
                  row.native_services.length
                    ? row
                    : {
                        ...row,
                        native_services: bulk.filter((service) => row.options.includes(service)),
                      },
                ),
              )
            }
          >
            {t('common.connections.applyToUnselected')}
          </button>
        </Fold>
      ) : null}
      {value.map((row, index) => (
        <fieldset key={identity(row)} className="nb-stack" disabled={disabled}>
          <legend>
            {index + 1}. {row.label}
          </legend>
          <strong className="nb-mono">{row.upstream_model_id}</strong>
          <NativeServicesField
            options={row.options}
            value={row.native_services}
            onChange={(native_services) => change(index, { native_services })}
          />
          {row.service_routes ? <ServiceRoutesSummary value={row.service_routes} /> : null}
          <div className="nb-actions">
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              disabled={index === 0}
              onClick={() => move(index, -1)}
            >
              {t('common.connections.moveUp')}
            </button>
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              disabled={index === value.length - 1}
              onClick={() => move(index, 1)}
            >
              {t('common.connections.moveDown')}
            </button>
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              onClick={() => onChange(value.filter((_, i) => i !== index))}
            >
              {t('common.connections.removeTarget')}
            </button>
          </div>
        </fieldset>
      ))}
      <Fold title={copy.addSources}>
        <PersonalBindingPicker
          accountId={accountId}
          modelId={modelId}
          selected={new Set(value.map(identity))}
          bound={bound}
          locked={disabled}
          onToggle={(candidate) => {
            if (value.some((row) => identity(row) === identity(candidate)))
              onChange(value.filter((row) => identity(row) !== identity(candidate)));
            else add(candidate);
          }}
        />
      </Fold>
    </div>
  );
}
