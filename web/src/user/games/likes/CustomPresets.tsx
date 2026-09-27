import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { createIdempotencyKey, isConflict, isResponseUnknown } from '../common/request';
import { useDuelText } from '../common/duel/copy';
import type { ModeCatalog } from './catalog';
import type { Selection } from './types';
import { selectionProblem } from './selection';
import {
  fetchCustomPresets,
  saveCustomPreset,
  type CustomPresetList,
  type SavedPreset,
  type SavePresetIntent,
} from './presetApi';

const presetKey = ['user', 'games', 'likes', 'custom-presets'] as const;

export function CustomPresets({
  catalogs,
  mode,
  selection,
  blocked,
  onLoad,
}: {
  readonly catalogs: Readonly<Record<'quick' | 'standard', ModeCatalog>>;
  readonly mode: 'quick' | 'standard';
  readonly selection: Selection;
  readonly blocked: boolean;
  readonly onLoad: (mode: 'quick' | 'standard', selection: Selection) => void;
}) {
  const t = useDuelText();
  const client = useQueryClient();
  const query = useQuery({
    queryKey: presetKey,
    queryFn: ({ signal }) => fetchCustomPresets(signal),
    retry: false,
    staleTime: 0,
    gcTime: 0,
  });
  const [overwrite, setOverwrite] = useState<SavedPreset | null>(null);
  const [pending, setPending] = useState<SavePresetIntent | null>(null);
  const [uncertain, setUncertain] = useState<SavePresetIntent | null>(null);
  const [notice, setNotice] = useState<{ error: boolean; text: string } | null>(null);
  const validSelection = !selectionProblem(catalogs[mode], selection);
  const unavailable = blocked || !!pending;
  const controlsDisabled = unavailable || !!uncertain;
  const slots = query.isSuccess && query.isFetchedAfterMount ? query.data.slots : undefined;

  const slotName = (slot: number) => t(`预设${slot}`, `Preset${slot}`);
  const makeIntent = (slot: number, expectedRevision: string): SavePresetIntent => ({
    slot,
    expectedRevision,
    mode,
    loadout: { role: selection.role, harness: selection.harness, skills: [...selection.skills] },
    key: createIdempotencyKey(),
  });
  const runSave = async (intent: SavePresetIntent) => {
    if (blocked || pending || (uncertain && uncertain.key !== intent.key)) return;
    setPending(intent);
    setNotice(null);
    try {
      const saved = await saveCustomPreset(intent);
      client.setQueryData<CustomPresetList>(presetKey, (previous) => {
        if (!previous) return previous;
        return {
          capacity: 10,
          slots: [...previous.slots.filter((item) => item.slot !== saved.slot), saved].sort(
            (a, b) => a.slot - b.slot,
          ),
        };
      });
      setUncertain(null);
      setNotice({
        error: false,
        text: t(
          `自定义预设${saved.slot}已${intent.expectedRevision === '0' ? '保存' : '覆盖'}。`,
          `Custom presets: ${slotName(saved.slot)} ${intent.expectedRevision === '0' ? 'saved' : 'overwritten'}.`,
        ),
      });
      void query.refetch();
    } catch (error) {
      if (isConflict(error)) {
        setUncertain(null);
        setNotice({
          error: true,
          text: t(
            '自定义预设已在其他位置变更。已重新读取槽位，请核对后再保存。',
            'Custom presets changed elsewhere. Check the refreshed slots before saving again.',
          ),
        });
        void query.refetch();
      } else if (isResponseUnknown(error)) {
        setUncertain(intent);
        setNotice({
          error: true,
          text: t(
            '自定义预设保存结果未确认。可用相同请求安全重试。',
            'Custom presets save could not be confirmed. Retry the same request safely.',
          ),
        });
      } else {
        setUncertain(null);
        setNotice({
          error: true,
          text: t(
            '自定义预设未保存。请检查当前配装与规则后重试。',
            'Custom presets were not saved. Check your loadout against the current rules and retry.',
          ),
        });
      }
    } finally {
      setPending(null);
    }
  };

  const load = (item: SavedPreset) => {
    if (controlsDisabled) return;
    const catalog = catalogs[item.mode];
    const selection = item.loadout as Selection;
    if (selectionProblem(catalog, selection)) {
      setNotice({
        error: true,
        text: t(
          `自定义预设${item.slot}不再符合当前规则，请重新配装后保存。`,
          `Custom presets: ${slotName(item.slot)} no longer fits the current rules. Edit your loadout and save it again.`,
        ),
      });
      return;
    }
    onLoad(item.mode, {
      role: selection.role,
      harness: selection.harness,
      skills: [...selection.skills],
    });
    setNotice({
      error: false,
      text: t(
        `已加载自定义预设${item.slot}，尚未进入匹配。`,
        `Custom presets: ${slotName(item.slot)} loaded. You have not joined matchmaking.`,
      ),
    });
  };

  return (
    <section className="likes-custom-presets" aria-label={t('自定义预设', 'Custom presets')}>
      <div className="likes-section-heading">
        <h2>{t('自定义预设', 'Custom presets')}</h2>
        <span>
          {t('账号保存10个配装，跨设备可用', 'Save 10 loadouts to your account across devices')}
        </span>
      </div>
      {!query.isFetchedAfterMount && !query.isError && (
        <p role="status">{t('正在读取自定义预设…', 'Loading custom presets…')}</p>
      )}
      {query.isError && (
        <p role="alert">
          {t(
            '自定义预设暂时无法读取。当前配装仍可编辑。',
            'Custom presets could not be loaded. You can still edit this loadout.',
          )}{' '}
          <button type="button" onClick={() => void query.refetch()} disabled={unavailable}>
            {t('重试读取', 'Retry loading')}
          </button>
        </p>
      )}
      {!validSelection && (
        <p>
          {t(
            '当前配装不符合规则，暂不能保存为自定义预设。',
            'This loadout does not meet the rules yet, so it cannot be saved as a custom preset.',
          )}
        </p>
      )}
      <div className="likes-custom-presets-grid">
        {Array.from({ length: 10 }, (_, index) => {
          const slot = index + 1;
          const item = slots?.find((entry) => entry.slot === slot);
          return (
            <div className="likes-custom-preset" key={slot}>
              <div>
                <strong>{slotName(slot)}</strong>
                <span>
                  {item
                    ? `${item.mode === 'quick' ? t('快速', 'Quick') : t('标准', 'Standard')} · ${item.loadout.role}`
                    : t('空槽位', 'Empty slot')}
                </span>
              </div>
              <div className="likes-custom-preset-actions">
                <button
                  type="button"
                  disabled={!item || controlsDisabled}
                  onClick={() => item && load(item)}
                >
                  {t(`加载${slotName(slot)}`, `Load ${slotName(slot)}`)}
                </button>
                <button
                  type="button"
                  disabled={!slots || !validSelection || controlsDisabled}
                  onClick={() => (item ? setOverwrite(item) : void runSave(makeIntent(slot, '0')))}
                >
                  {item
                    ? t(`覆盖${slotName(slot)}`, `Overwrite ${slotName(slot)}`)
                    : t(`保存到${slotName(slot)}`, `Save to ${slotName(slot)}`)}
                </button>
              </div>
            </div>
          );
        })}
      </div>
      {uncertain && (
        <button type="button" disabled={unavailable} onClick={() => void runSave(uncertain)}>
          {t('重试保存自定义预设', 'Retry saving custom presets')}
        </button>
      )}
      {notice && <p role={notice.error ? 'alert' : 'status'}>{notice.text}</p>}
      <ConfirmDialog
        open={!!overwrite}
        title={t('覆盖自定义预设', 'Overwrite custom presets')}
        description={t(
          `这会用当前配装覆盖${overwrite ? slotName(overwrite.slot) : ''}。`,
          `This replaces ${overwrite ? slotName(overwrite.slot) : ''} with your current loadout.`,
        )}
        confirmLabel={t('确认覆盖', 'Confirm overwrite')}
        cancelLabel={t('取消', 'Cancel')}
        busy={!!pending}
        onCancel={() => setOverwrite(null)}
        onConfirm={() => {
          if (overwrite) void runSave(makeIntent(overwrite.slot, overwrite.revision));
          setOverwrite(null);
        }}
      />
    </section>
  );
}
