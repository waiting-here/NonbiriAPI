import { useEffect, useId, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import '@shared/operations/operations.css';
import {
  buildRolePolicy,
  roleActions,
  type RoleAction,
  type RolePolicyDraft,
} from '@shared/rolePolicy';

const actionKeys: Record<RoleAction, string> = {
  native: 'common.rolePolicy.action.native',
  passthrough: 'common.rolePolicy.action.passthrough',
  system: 'common.rolePolicy.action.system',
  user: 'common.rolePolicy.action.user',
  assistant: 'common.rolePolicy.action.assistant',
  reject: 'common.rolePolicy.action.reject',
};
const errorKeys = {
  name: 'common.rolePolicy.validation.name',
  reserved: 'common.rolePolicy.validation.reserved',
  duplicate: 'common.rolePolicy.validation.duplicate',
  count: 'common.rolePolicy.validation.count',
} as const;

export interface RolePolicyEditorProps {
  value: RolePolicyDraft;
  onChange: (value: RolePolicyDraft) => void;
  disabled?: boolean;
  idPrefix?: string;
}

export function RolePolicyEditor({ value, onChange, disabled, idPrefix }: RolePolicyEditorProps) {
  const { t } = useTranslation();
  const generated = useId();
  const id = idPrefix ?? generated;
  const inputRefs = useRef<(HTMLInputElement | null)[]>([]);
  const addButton = useRef<HTMLButtonElement>(null);
  const pendingFocus = useRef<number | null>(null);
  const disclosure = useRef<HTMLDetailsElement>(null);
  useEffect(() => {
    if (pendingFocus.current === null) return;
    (inputRefs.current[pendingFocus.current] ?? addButton.current)?.focus();
    pendingFocus.current = null;
  }, [value.rules]);
  const error = buildRolePolicy(value).error;
  const hasError = Boolean(error);
  useEffect(() => {
    if (hasError && disclosure.current) disclosure.current.open = true;
  }, [hasError]);
  const updateRow = (index: number, patch: Partial<RolePolicyDraft['rules'][number]>) =>
    onChange({
      ...value,
      rules: value.rules.map((row, current) => (current === index ? { ...row, ...patch } : row)),
    });
  return (
    <details className="nb-fold nb-fold--plain ops-advanced" ref={disclosure}>
      <summary>
        <span className="nb-fold__title">
          <strong>{t('common.rolePolicy.title')}</strong>
          {' · '}
          {t('common.rolePolicy.summary', {
            action: t(actionKeys[value.defaultAction]),
            count: value.rules.length,
          })}
        </span>
      </summary>
      <fieldset
        className="nb-fold__body ops-form-section"
        disabled={disabled}
        aria-describedby={`${id}-help`}
      >
        <legend>{t('common.rolePolicy.title')}</legend>
        <p id={`${id}-help`}>{t('common.rolePolicy.help')}</p>
        <label>
          <span>{t('common.rolePolicy.defaultAction')}</span>
          <select
            value={value.defaultAction}
            onChange={(event) =>
              onChange({ ...value, defaultAction: event.target.value as RoleAction })
            }
          >
            {roleActions.map((action) => (
              <option key={action} value={action}>
                {t(actionKeys[action])}
              </option>
            ))}
          </select>
        </label>
        <small>{t('common.rolePolicy.nativeHelp')}</small>
        <div className="ops-stack">
          {value.rules.map((row, index) => (
            <div className="ops-subcard" key={index}>
              <div className="ops-field-grid">
                <label>
                  <span>{t('common.rolePolicy.roleName')}</span>
                  <input
                    ref={(element) => {
                      inputRefs.current[index] = element;
                    }}
                    value={row.role}
                    placeholder="developer"
                    aria-describedby={`${id}-name-help`}
                    aria-invalid={error?.row === index}
                    onChange={(event) => updateRow(index, { role: event.target.value })}
                  />
                </label>
                <label>
                  <span>{t('common.rolePolicy.roleAction')}</span>
                  <select
                    value={row.action}
                    onChange={(event) =>
                      updateRow(index, { action: event.target.value as RoleAction })
                    }
                  >
                    {roleActions.map((action) => (
                      <option key={action} value={action}>
                        {t(actionKeys[action])}
                      </option>
                    ))}
                  </select>
                </label>
              </div>
              <button
                type="button"
                className="btn btn-quiet"
                onClick={() => {
                  pendingFocus.current = Math.min(index, value.rules.length - 2);
                  onChange({
                    ...value,
                    rules: value.rules.filter((_, current) => current !== index),
                  });
                }}
              >
                {t('common.rolePolicy.removeRule', { number: index + 1 })}
              </button>
              {error?.row === index ? (
                <p className="field-error" role="alert">
                  {t(errorKeys[error.kind])}
                </p>
              ) : null}
            </div>
          ))}
        </div>
        <p id={`${id}-name-help`} className="muted">
          {t('common.rolePolicy.nameHelp')}
        </p>
        <button
          ref={addButton}
          type="button"
          className="btn btn-secondary"
          disabled={value.rules.length >= 32}
          onClick={() => {
            pendingFocus.current = value.rules.length;
            onChange({ ...value, rules: [...value.rules, { role: '', action: 'native' }] });
          }}
        >
          {t('common.rolePolicy.addRule')}
        </button>
        {error?.kind === 'count' ? (
          <p className="field-error" role="alert">
            {t(errorKeys.count)}
          </p>
        ) : null}
        <p className="muted">{t('common.rolePolicy.conversionHelp')}</p>
        <p className="muted">{t('common.rolePolicy.toolsHelp')}</p>
      </fieldset>
    </details>
  );
}
