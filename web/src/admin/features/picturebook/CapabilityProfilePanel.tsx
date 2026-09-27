import { forwardRef, useEffect, useImperativeHandle, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { stationSessionWrite } from '@shared/charityManagement';
import { Card, ErrorState, LoadingState } from '@shared/components/States';
import { usePictureBookText } from '@shared/picturebook/copy';
import { useImageOperation } from '@shared/picturebook/useImageOperation';
import { parameterKeys, type ParameterKey } from '@shared/picturebook/publicTypes';
import { getCapabilityProfile, saveCapabilityProfile } from './adminApi';

const emptyProfile = {
  version: 1,
  fields: [
    {
      rule: {
        key: 'prompt',
        type: 'string',
        supported: true,
        required: true,
        length_unit: 'utf8_bytes',
        min_length: 1,
        max_length: 65536,
      },
    },
    {
      rule: {
        key: 'n',
        type: 'integer',
        supported: true,
        required: false,
        minimum: 1,
        maximum: 16,
        default: 1,
      },
    },
  ],
};

type ProfileDraft = {
  fields: Record<string, unknown>[];
  size?: {
    capability: Record<string, unknown>;
    combinations_pointer?: string;
    auto_pointer?: string;
  };
  catalog_type_pointer?: string;
  image_values?: string[];
};

export interface ProfileDraftHandle {
  saveDraft: () => Promise<boolean>;
  discardDraft: () => void;
}

function optionalNumber(raw: string): number | undefined {
  if (!raw.trim()) return undefined;
  const value = Number(raw);
  if (!Number.isSafeInteger(value)) throw new Error('invalid integer');
  return value;
}

function ProfileSizeControls({
  size,
  mutate,
  onInvalid,
  disabled,
}: {
  readonly size: ProfileDraft['size'];
  readonly mutate: (change: (profile: ProfileDraft) => void) => void;
  readonly onInvalid: () => void;
  readonly disabled: boolean;
}) {
  const t = usePictureBookText();
  const capability = size?.capability ?? {};
  const mode = String(capability.mode ?? 'resolution_ratio_grid');
  const rows = Array.isArray(capability.combinations)
    ? (capability.combinations as Record<string, unknown>[])
    : [];
  const editSize = (change: (value: NonNullable<ProfileDraft['size']>) => void) =>
    mutate((profile) => {
      if (profile.size) change(profile.size);
    });
  const editCapability = (key: string, value: unknown) =>
    editSize((valueSize) => {
      if (value === undefined) delete valueSize.capability[key];
      else valueSize.capability[key] = value;
    });
  const numeric = (raw: string, set: (value: number | undefined) => void) => {
    try {
      set(optionalNumber(raw));
    } catch {
      onInvalid();
    }
  };
  return (
    <fieldset className="picturebook-form" disabled={disabled}>
      <legend>{t('尺寸能力提取', 'Size capability extraction')}</legend>
      <label className="picturebook-checkbox">
        <input
          type="checkbox"
          checked={!!size}
          onChange={(event) =>
            mutate((profile) => {
              if (event.target.checked)
                profile.size = {
                  capability: { mode: 'resolution_ratio_grid', combinations: [], auto: true },
                };
              else delete profile.size;
            })
          }
        />
        {t('提取尺寸能力', 'Extract size capability')}
      </label>
      {size ? (
        <>
          <div className="picturebook-grid">
            <label>
              {t('尺寸转换模式', 'Size converter mode')}
              <select
                value={mode}
                onChange={(event) =>
                  editSize((value) => {
                    value.capability = {
                      mode: event.target.value,
                      combinations: [],
                      ...(event.target.value === 'width_height'
                        ? {
                            width: { minimum: 1, maximum: 1024, step: 1 },
                            height: { minimum: 1, maximum: 1024, step: 1 },
                          }
                        : {}),
                    };
                  })
                }
              >
                <option value="resolution_ratio_grid">resolution_ratio_grid</option>
                <option value="ratio_size_map">ratio_size_map</option>
                <option value="ratio_resolution">ratio_resolution</option>
                <option value="width_height">width_height</option>
              </select>
            </label>
            {(['combinations_pointer', 'auto_pointer'] as const).map((key) => (
              <label key={key}>
                {key}
                <input
                  value={size[key] ?? ''}
                  onChange={(event) =>
                    editSize((value) => {
                      if (event.target.value) value[key] = event.target.value;
                      else delete value[key];
                    })
                  }
                  maxLength={512}
                  spellCheck={false}
                />
              </label>
            ))}
            <label className="picturebook-checkbox">
              <input
                type="checkbox"
                checked={capability.auto === true}
                onChange={(event) => editCapability('auto', event.target.checked || undefined)}
              />
              {t('支持自动尺寸', 'Supports automatic size')}
            </label>
          </div>
          {mode === 'width_height' ? (
            <div className="picturebook-grid">
              {(['width', 'height'] as const).flatMap((axis) =>
                (['minimum', 'maximum', 'step'] as const).map((key) => {
                  const range = (capability[axis] ?? {}) as Record<string, unknown>;
                  return (
                    <label key={axis + key}>
                      {axis}.{key}
                      <input
                        type="number"
                        value={range[key] === undefined ? '' : String(range[key])}
                        onChange={(event) =>
                          numeric(event.target.value, (value) =>
                            editSize((item) => {
                              const target = (item.capability[axis] ?? {}) as Record<
                                string,
                                unknown
                              >;
                              if (value === undefined) delete target[key];
                              else target[key] = value;
                              item.capability[axis] = target;
                            }),
                          )
                        }
                      />
                    </label>
                  );
                }),
              )}
              <label>
                max_pixels
                <input
                  type="number"
                  value={capability.max_pixels === undefined ? '' : String(capability.max_pixels)}
                  onChange={(event) =>
                    numeric(event.target.value, (value) => editCapability('max_pixels', value))
                  }
                />
              </label>
            </div>
          ) : null}
          <p>{t('可手动维护已发现的尺寸组合。', 'Maintain discovered size combinations.')}</p>
          {rows.map((row, index) => (
            <div className="picturebook-grid" key={index}>
              {(['ratio', 'resolution', 'tier', 'width', 'height'] as const).map((key) => (
                <label key={key}>
                  {t('组合', 'Combination')} {index + 1} {key}
                  <input
                    type={key === 'width' || key === 'height' ? 'number' : 'text'}
                    value={row[key] === undefined ? '' : String(row[key])}
                    onChange={(event) => {
                      const raw = event.target.value;
                      const update = (next: unknown) =>
                        editSize((item) => {
                          const target = (
                            item.capability.combinations as Record<string, unknown>[]
                          )[index];
                          if (next === undefined) delete target[key];
                          else target[key] = next;
                        });
                      if (key === 'width' || key === 'height') numeric(raw, update);
                      else update(raw || undefined);
                    }}
                  />
                </label>
              ))}
              <button
                className="btn btn-secondary"
                type="button"
                onClick={() =>
                  editSize((item) =>
                    (item.capability.combinations as Record<string, unknown>[]).splice(index, 1),
                  )
                }
              >
                {t('移除组合', 'Remove combination')}
              </button>
            </div>
          ))}
          <button
            className="btn btn-secondary"
            type="button"
            disabled={rows.length >= 2048}
            onClick={() =>
              editSize((item) => {
                const list = (item.capability.combinations ?? []) as Record<string, unknown>[];
                list.push(
                  mode === 'width_height'
                    ? { width: 1, height: 1 }
                    : mode === 'ratio_size_map'
                      ? { ratio: '1:1', width: 1024, height: 1024 }
                      : mode === 'ratio_resolution'
                        ? { ratio: '1:1', resolution: '1024' }
                        : { ratio: '1:1', resolution: '1024', width: 1024, height: 1024 },
                );
                item.capability.combinations = list;
              })
            }
          >
            {t('添加尺寸组合', 'Add size combination')}
          </button>
        </>
      ) : null}
    </fieldset>
  );
}

const ProfileEditor = forwardRef<
  ProfileDraftHandle,
  {
    readonly revision: string;
    readonly initial: unknown;
    readonly onSaved: () => void;
    readonly onDirty: (dirty: boolean) => void;
  }
>(function ProfileEditor({ revision, initial, onSaved, onDirty }, ref) {
  const t = usePictureBookText();
  const [draft, setDraft] = useState(() => JSON.stringify(initial ?? emptyProfile, null, 2));
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState('');
  const [newKey, setNewKey] = useState<ParameterKey>('size');
  const save = useImageOperation('admin', saveCapabilityProfile);
  const mutate = (change: (profile: ProfileDraft) => void) => {
    try {
      const parsed = JSON.parse(draft) as ProfileDraft;
      if (!Array.isArray(parsed.fields)) throw new Error();
      change(parsed);
      setDraft(JSON.stringify(parsed, null, 2));
      setError('');
      setSaved(false);
    } catch {
      setError(t('请先修正JSON格式。', 'Fix the JSON syntax first.'));
    }
  };
  const editField = (index: number, property: string, value: string) => {
    mutate((parsed) => {
      if (!parsed.fields[index]) return;
      if (value) parsed.fields[index][property] = value;
      else delete parsed.fields[index][property];
    });
  };
  const editRule = (index: number, property: string, value: unknown) =>
    mutate((parsed) => {
      const field = parsed.fields[index];
      if (!field || !field.rule || typeof field.rule !== 'object') return;
      const rule = field.rule as Record<string, unknown>;
      if (value === undefined) delete rule[property];
      else rule[property] = value;
    });
  const editJSONRule = (index: number, property: string, raw: string) => {
    if (!raw.trim()) {
      editRule(index, property, undefined);
      return;
    }
    try {
      editRule(index, property, JSON.parse(raw) as unknown);
    } catch {
      setError(t('参数值须为有效JSON。', 'The parameter value must be valid JSON.'));
    }
  };
  const editNumericRule = (index: number, property: string, raw: string) => {
    if (!raw.trim()) {
      editRule(index, property, undefined);
      return;
    }
    const number = Number(raw);
    if (!Number.isFinite(number)) {
      setError(t('数值须为有限数字。', 'Enter a finite number.'));
      return;
    }
    editRule(index, property, number);
  };
  let fields: Record<string, unknown>[] = [];
  let profile: ProfileDraft | null = null;
  try {
    const parsed = JSON.parse(draft) as ProfileDraft;
    profile = parsed;
    if (Array.isArray(parsed.fields)) fields = parsed.fields as Record<string, unknown>[];
  } catch {
    /* Keep the draft editable after an import error. */
  }
  const initialText = JSON.stringify(initial ?? emptyProfile, null, 2);
  useEffect(() => onDirty(!saved && draft !== initialText), [draft, initialText, onDirty, saved]);
  const saveDraft = async (): Promise<boolean> => {
    if (saved || draft === initialText) return true;
    let accepted = false;
    if (save.uncertain && save.input) {
      await save.run(save.input, () => {
        accepted = true;
        setSaved(true);
        onSaved();
      });
      return accepted;
    }
    try {
      const parsed = JSON.parse(draft) as unknown;
      if (new TextEncoder().encode(draft).byteLength > 262144) throw new Error();
      setError('');
      await save.run({ expected_revision: revision, profile: parsed }, () => {
        accepted = true;
        setSaved(true);
        onSaved();
      });
      return accepted;
    } catch {
      setError(
        t('配置须为不超过256 KiB的有效JSON。', 'The profile must be valid JSON within 256 KiB.'),
      );
      return false;
    }
  };
  useImperativeHandle(ref, () => ({
    saveDraft,
    discardDraft: () => {
      setDraft(initialText);
      setSaved(false);
      setError('');
    },
  }));
  return (
    <Card>
      <h3>{t('模型能力提取配置', 'Model capability profile')}</h3>
      <p>
        {t(
          '此配置仅提取已保存目录元数据；编辑和检查不会发起生图。导出的JSON不含服务地址或密钥。',
          'This profile reads saved catalog metadata only. Editing and checking never generate images. The exported JSON contains no service URL or key.',
        )}
      </p>
      <p>
        {t('当前修订：', 'Current revision: ')}
        {revision}
      </p>
      <div className="picturebook-stack">
        <fieldset className="picturebook-form" disabled={save.locked || saved}>
          <legend>{t('目录分类提取', 'Catalog classification extraction')}</legend>
          <label>
            catalog_type_pointer
            <input
              value={profile?.catalog_type_pointer ?? ''}
              maxLength={512}
              spellCheck={false}
              onChange={(event) =>
                mutate((parsed) => {
                  if (event.target.value) {
                    parsed.catalog_type_pointer = event.target.value;
                    parsed.image_values ??= ['image'];
                  } else {
                    delete parsed.catalog_type_pointer;
                    delete parsed.image_values;
                  }
                })
              }
            />
          </label>
          {profile?.catalog_type_pointer ? (
            <>
              {(profile.image_values ?? []).map((item, index) => (
                <div className="picturebook-actions" key={index}>
                  <label>
                    {t('图像分类值', 'Image classification value')} {index + 1}
                    <input
                      value={item}
                      maxLength={128}
                      onChange={(event) =>
                        mutate((parsed) => {
                          parsed.image_values![index] = event.target.value;
                        })
                      }
                    />
                  </label>
                  <button
                    className="btn btn-secondary"
                    type="button"
                    onClick={() =>
                      mutate((parsed) => {
                        parsed.image_values?.splice(index, 1);
                      })
                    }
                  >
                    {t('移除分类值', 'Remove classification value')}
                  </button>
                </div>
              ))}
              <button
                className="btn btn-secondary"
                type="button"
                disabled={(profile.image_values?.length ?? 0) >= 128}
                onClick={() =>
                  mutate((parsed) => {
                    (parsed.image_values ??= []).push('image');
                  })
                }
              >
                {t('添加图像分类值', 'Add image classification value')}
              </button>
            </>
          ) : null}
        </fieldset>
        <ProfileSizeControls
          size={profile?.size}
          mutate={mutate}
          onInvalid={() => setError(t('数值须为安全整数。', 'Enter a safe integer.'))}
          disabled={save.locked || saved}
        />
        {fields.map((field, index) => {
          const rule =
            field.rule && typeof field.rule === 'object'
              ? (field.rule as Record<string, unknown>)
              : {};
          return (
            <fieldset key={index} disabled={save.locked || saved}>
              <legend>
                {t('参数提取', 'Parameter extraction')} · {String(rule.key ?? index + 1)}
              </legend>
              <div className="picturebook-grid">
                <label>
                  {t('类型', 'Type')}
                  <select
                    value={String(rule.type ?? 'string')}
                    onChange={(event) => editRule(index, 'type', event.target.value)}
                  >
                    <option value="string">string</option>
                    <option value="integer">integer</option>
                    <option value="number">number</option>
                  </select>
                </label>
                <label className="picturebook-checkbox">
                  <input
                    type="checkbox"
                    checked={rule.supported === true}
                    onChange={(event) => editRule(index, 'supported', event.target.checked)}
                  />
                  {t('已明确支持', 'Explicitly supported')}
                </label>
                <label className="picturebook-checkbox">
                  <input
                    type="checkbox"
                    checked={rule.required === true}
                    onChange={(event) => editRule(index, 'required', event.target.checked)}
                  />
                  {t('必填', 'Required')}
                </label>
                {rule.type === 'string' ? (
                  <label>
                    {t('长度单位', 'Length unit')}
                    <select
                      value={String(rule.length_unit ?? 'utf8_bytes')}
                      onChange={(event) => editRule(index, 'length_unit', event.target.value)}
                    >
                      <option value="utf8_bytes">UTF-8 bytes</option>
                      <option value="unicode_scalars">Unicode scalars</option>
                      <option value="utf16_units">UTF-16 units</option>
                    </select>
                  </label>
                ) : null}
              </div>
              <div className="picturebook-grid">
                {(rule.type === 'string'
                  ? ['min_length', 'max_length']
                  : ['minimum', 'maximum', 'step']
                ).map((property) => (
                  <label key={property}>
                    {property}
                    <input
                      key={index + ':' + property + ':' + String(rule[property] ?? '')}
                      type="number"
                      step={property.includes('length') ? '1' : 'any'}
                      defaultValue={rule[property] === undefined ? '' : String(rule[property])}
                      onBlur={(event) => editNumericRule(index, property, event.target.value)}
                    />
                  </label>
                ))}
              </div>
              {(['default', 'enum'] as const).map((property) => (
                <label key={property}>
                  {property} JSON
                  <input
                    key={index + ':' + property + ':' + JSON.stringify(rule[property])}
                    defaultValue={
                      rule[property] === undefined ? '' : JSON.stringify(rule[property])
                    }
                    onBlur={(event) => editJSONRule(index, property, event.target.value)}
                    spellCheck={false}
                  />
                </label>
              ))}
              {rule.key === 'size' && rule.type === 'string' ? (
                <fieldset>
                  <legend>{t('宽高尺寸规则', 'Width and height size rule')}</legend>
                  <label className="picturebook-checkbox">
                    <input
                      type="checkbox"
                      checked={!!rule.dimensions}
                      onChange={(event) =>
                        editRule(
                          index,
                          'dimensions',
                          event.target.checked
                            ? {
                                format: 'width_height',
                                width: { minimum: 1, maximum: 1024, step: 1 },
                                height: { minimum: 1, maximum: 1024, step: 1 },
                              }
                            : undefined,
                        )
                      }
                    />
                    {t('解析宽高', 'Parse width and height')}
                  </label>
                  {rule.dimensions && typeof rule.dimensions === 'object' ? (
                    <div className="picturebook-grid">
                      {(['width', 'height'] as const).flatMap((axis) =>
                        (['minimum', 'maximum', 'step'] as const).map((key) => {
                          const dimensions = rule.dimensions as Record<string, unknown>;
                          const range = (dimensions[axis] ?? {}) as Record<string, unknown>;
                          return (
                            <label key={axis + key}>
                              dimensions.{axis}.{key}
                              <input
                                type="number"
                                value={range[key] === undefined ? '' : String(range[key])}
                                onChange={(event) => {
                                  try {
                                    const value = optionalNumber(event.target.value);
                                    mutate((parsed) => {
                                      const target = (
                                        (parsed.fields[index].rule as Record<string, unknown>)
                                          .dimensions as Record<string, unknown>
                                      )[axis] as Record<string, unknown>;
                                      if (value === undefined) delete target[key];
                                      else target[key] = value;
                                    });
                                  } catch {
                                    setError(t('数值须为安全整数。', 'Enter a safe integer.'));
                                  }
                                }}
                              />
                            </label>
                          );
                        }),
                      )}
                    </div>
                  ) : null}
                </fieldset>
              ) : null}
              {(
                [
                  'support_pointer',
                  'enum_pointer',
                  'default_pointer',
                  'minimum_pointer',
                  'maximum_pointer',
                  'step_pointer',
                ] as const
              ).map((property) => (
                <label key={property}>
                  {property}
                  <input
                    value={typeof field[property] === 'string' ? (field[property] as string) : ''}
                    onChange={(event) => editField(index, property, event.target.value)}
                    maxLength={512}
                    spellCheck={false}
                  />
                </label>
              ))}
              <label>
                support_equals JSON
                <input
                  key={index + ':support_equals:' + JSON.stringify(field.support_equals)}
                  defaultValue={
                    field.support_equals === undefined ? '' : JSON.stringify(field.support_equals)
                  }
                  onBlur={(event) => {
                    if (!event.target.value.trim()) editField(index, 'support_equals', '');
                    else {
                      try {
                        mutate((parsed) => {
                          parsed.fields[index].support_equals = JSON.parse(
                            event.target.value,
                          ) as unknown;
                        });
                      } catch {
                        setError(t('比较值须为有效JSON。', 'Comparison value must be valid JSON.'));
                      }
                    }
                  }}
                  spellCheck={false}
                />
              </label>
              <button
                className="btn btn-secondary"
                type="button"
                onClick={() =>
                  mutate((parsed) => {
                    parsed.fields.splice(index, 1);
                  })
                }
              >
                {t('移除此参数', 'Remove parameter')}
              </button>
            </fieldset>
          );
        })}
        <div className="picturebook-actions">
          <select
            aria-label={t('添加参数', 'Add parameter')}
            value={newKey}
            onChange={(event) => setNewKey(event.target.value as ParameterKey)}
          >
            {parameterKeys
              .filter(
                (key) =>
                  !fields.some(
                    (field) => (field.rule as Record<string, unknown> | undefined)?.key === key,
                  ),
              )
              .map((key) => (
                <option key={key} value={key}>
                  {key}
                </option>
              ))}
          </select>
          <button
            className="btn btn-secondary"
            type="button"
            disabled={
              fields.length >= 10 ||
              fields.some(
                (field) => (field.rule as Record<string, unknown> | undefined)?.key === newKey,
              ) ||
              save.locked ||
              saved
            }
            onClick={() =>
              mutate((parsed) => {
                const numeric = ['n', 'seed', 'steps', 'guidance'].includes(newKey);
                parsed.fields.push({
                  rule: {
                    key: newKey,
                    type: numeric ? (newKey === 'guidance' ? 'number' : 'integer') : 'string',
                    supported: true,
                    required: newKey === 'prompt',
                    ...(numeric ? {} : { length_unit: 'utf8_bytes' }),
                  },
                });
              })
            }
          >
            {t('添加参数', 'Add parameter')}
          </button>
        </div>
      </div>
      <label>
        {t(
          '完整声明式配置JSON（导入、导出和高级编辑）',
          'Complete declarative profile JSON (import, export, advanced edit)',
        )}
        <textarea
          className="picturebook-json"
          spellCheck={false}
          disabled={save.locked || saved}
          value={draft}
          onChange={(event) => {
            setDraft(event.target.value);
            setSaved(false);
            setError('');
          }}
        />
      </label>
      <div className="picturebook-actions">
        <label>
          {t('导入能力配置JSON', 'Import capability profile JSON')}
          <input
            type="file"
            accept="application/json,.json"
            disabled={save.locked || saved}
            onChange={(event) => {
              const file = event.target.files?.[0];
              event.target.value = '';
              if (!file) return;
              if (file.size > 262144) {
                setError(t('配置文件超过256 KiB。', 'The profile file exceeds 256 KiB.'));
                return;
              }
              void file
                .text()
                .then((raw) => {
                  JSON.parse(raw);
                  setDraft(raw);
                  setSaved(false);
                  setError('');
                })
                .catch(() =>
                  setError(
                    t('配置文件须为有效JSON。', 'The profile file must contain valid JSON.'),
                  ),
                );
            }}
          />
        </label>
        <button
          className="btn btn-secondary"
          type="button"
          disabled={initial === null}
          onClick={() => {
            try {
              // Export only the server-validated saved profile, never an
              // unreviewed imported draft with arbitrary extra fields.
              const raw = JSON.stringify(initial, null, 2);
              if (new TextEncoder().encode(raw).byteLength > 262144) throw new Error();
              const href = URL.createObjectURL(
                new Blob([raw + '\n'], { type: 'application/json' }),
              );
              const link = document.createElement('a');
              link.href = href;
              link.download = 'picture-book-capability-profile.json';
              link.click();
              URL.revokeObjectURL(href);
            } catch {
              setError(t('无法导出已保存的配置。', 'Unable to export the saved profile.'));
            }
          }}
        >
          {t('导出能力配置JSON', 'Export capability profile JSON')}
        </button>
      </div>
      {error ? <p role="alert">{error}</p> : null}
      {save.error ? <ErrorState error={save.error} /> : null}
      {save.uncertain ? (
        <p role="status">
          {t(
            '保存结果未确认，请使用同一次操作重试。',
            'Save receipt unconfirmed. Retry the same operation.',
          )}
        </p>
      ) : null}
      {saved ? (
        <p role="status">{t('能力配置已保存并读回。', 'Capability profile saved and reloaded.')}</p>
      ) : null}
      <button
        className="btn btn-primary"
        type="button"
        disabled={save.pending || saved}
        onClick={() => void saveDraft()}
      >
        {save.uncertain
          ? t('重试同一次保存', 'Retry the same save')
          : t('保存能力配置', 'Save capability profile')}
      </button>
      <small>
        {t(
          '私有字段路径仅保留在实例配置中。离页前请保存或自行保留草稿。',
          'Private field paths stay in instance configuration. Save or retain your draft before leaving.',
        )}
      </small>
    </Card>
  );
});

export const CapabilityProfilePanel = forwardRef<
  ProfileDraftHandle,
  {
    readonly account: string;
    readonly onDirty: (dirty: boolean) => void;
  }
>(function CapabilityProfilePanel({ account, onDirty }, ref) {
  const client = useQueryClient();
  const key = ['admin', 'picture-book', account, 'capability-profile'] as const;
  const profile = useQuery({
    queryKey: key,
    queryFn: ({ signal }) =>
      stationSessionWrite(client, 'admin', () => getCapabilityProfile(signal)),
    refetchOnWindowFocus: false,
    retry: false,
  });
  if (profile.isPending) return <LoadingState />;
  if (profile.error)
    return <ErrorState error={profile.error} onRetry={() => void profile.refetch()} />;
  return (
    <ProfileEditor
      ref={ref}
      key={profile.data.revision}
      revision={profile.data.revision}
      initial={profile.data.profile}
      onDirty={onDirty}
      onSaved={() => void client.invalidateQueries({ queryKey: key })}
    />
  );
});
