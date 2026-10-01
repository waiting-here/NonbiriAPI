import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { stationSessionWrite } from '@shared/charityManagement';
import { useOperation } from '@shared/operations/useOperation';
import { useUserSession } from '../../data';
import { useDuelText } from '../common/duel/copy';
import type { ModeCatalog } from './catalog';
import type { Selection } from './types';
import { selectionProblem } from './selection';
import {
  fetchCustomPresets,
  saveCustomPreset,
  renameCustomPreset,
  type CustomPresetList,
  type SavedPreset,
  type SavePresetIntent,
  type RenamePresetIntent,
} from './presetApi';
type PresetIntent =
  | ({
      kind: 'save';
    } & Omit<SavePresetIntent, 'key'>)
  | ({
      kind: 'rename';
    } & Omit<RenamePresetIntent, 'key'>);
type PresetProps = {
  readonly catalogs: Readonly<Record<'quick' | 'standard', ModeCatalog>>;
  readonly mode: 'quick' | 'standard';
  readonly selection: Selection;
  readonly blocked: boolean;
  readonly onLoad: (mode: 'quick' | 'standard', selection: Selection) => void;
};
export function CustomPresets(props: PresetProps) {
  const session = useUserSession(false);
  const account = session.data?.user.id;
  return <AccountCustomPresets key={account ?? 'no-account'} {...props} account={account} />;
}
function AccountCustomPresets({
  catalogs,
  mode,
  selection,
  blocked,
  onLoad,
  account,
}: PresetProps & { account: string | undefined }) {
  const text = useDuelText();
  const client = useQueryClient();
  const presetKey = ['user', 'games', 'likes', 'custom-presets', account] as const;
  const query = useQuery({
    queryKey: presetKey,
    queryFn: ({ signal }) =>
      stationSessionWrite(client, 'steward', () => fetchCustomPresets(signal)),
    enabled: !!account,
    retry: false,
    staleTime: 0,
    gcTime: 0,
  });
  const [overwrite, setOverwrite] = useState<SavedPreset | null>(null);
  const [names, setNames] = useState<Record<number, string>>({});
  const [loaded, setLoaded] = useState<number | null>(null);
  const operation = useOperation<PresetIntent, never, SavedPreset>({
    authorityRoot: presetKey,
    execute: async (intent, _secret, key, context) => {
      const saved =
        intent.kind === 'rename'
          ? await renameCustomPreset({ ...intent, key }, context.signal)
          : await saveCustomPreset({ ...intent, key }, context.signal);
      context.commit(() => {
        client.setQueryData<CustomPresetList>(
          presetKey,
          (previous) =>
            previous && {
              capacity: 10,
              slots: [...previous.slots.filter((item) => item.slot !== saved.slot), saved].sort(
                (a, b) => a.slot - b.slot,
              ),
            },
        );
        setNames((previous) => {
          const next = { ...previous };
          delete next[saved.slot];
          return next;
        });
      });
      return saved;
    },
    reconcile: async (_intent, _error, context) => {
      const value = await fetchCustomPresets(context.signal);
      context.commit(() => client.setQueryData(presetKey, value));
    },
    clearSecrets: () => {
      setOverwrite(null);
      setNames({});
      setLoaded(null);
    },
  });
  const uncertain = operation.outcome === 'unknown';
  const unavailable = blocked || operation.isPending || operation.status === 'pending';
  const controlsDisabled = unavailable || uncertain;
  const validSelection = !selectionProblem(catalogs[mode], selection);
  const slots = query.isSuccess && query.isFetchedAfterMount ? query.data.slots : undefined;
  const slotName = (slot: number) => text('likes.preset', { slot: slot });
  const run = (intent: PresetIntent) => {
    if (unavailable) return;
    setLoaded(null);
    void operation.run(intent).catch(() => undefined);
  };
  const makeIntent = (slot: number, expectedRevision: string): PresetIntent => ({
    kind: 'save',
    slot,
    expectedRevision,
    mode,
    loadout: { ...selection, skills: [...selection.skills] },
  });
  const load = (item: SavedPreset) => {
    if (controlsDisabled || selectionProblem(catalogs[item.mode], item.loadout as Selection))
      return;
    onLoad(item.mode, {
      ...item.loadout,
      role: item.loadout.role as Selection['role'],
      skills: [...item.loadout.skills],
    });
    setLoaded(item.slot);
    operation.reset();
  };
  return (
    <section className="likes-custom-presets" aria-label={text('likes.customPresets')}>
      <div className="likes-section-heading">
        <h2>{text('likes.customPresets')}</h2>
        <span>{text('likes.save10LoadoutsToYourAccountAcross')}</span>
      </div>
      {!query.isFetchedAfterMount && !query.isError && (
        <p role="status">{text('likes.loadingCustomPresets')}</p>
      )}
      {query.isError && (
        <p role="alert">
          {text('likes.customPresetsCouldNotBeLoadedYou')}
          <button type="button" onClick={() => void query.refetch()} disabled={unavailable}>
            {text('likes.retryLoading')}
          </button>
        </p>
      )}
      {!validSelection && <p>{text('likes.thisLoadoutDoesNotMeetTheRules')}</p>}
      <div className="likes-custom-presets-grid">
        {Array.from({ length: 10 }, (_, index) => {
          const slot = index + 1,
            item = slots?.find((entry) => entry.slot === slot);
          const catalog = item && catalogs[item.mode];
          const invalid = !!item && !!selectionProblem(catalog!, item.loadout as Selection);
          const draft = names[slot] ?? item?.name ?? '';
          const nameInvalid = [...draft].length > 20 || /[\p{Cc}\p{Zl}\p{Zp}\p{Cs}]/u.test(draft);
          const describe = (id: string, name: string | undefined) =>
            name ?? text('likes.unavailable', { id: id });
          const rename = (name: string) =>
            item && void run({ kind: 'rename', slot, name, expectedRevision: item.revision });
          return (
            <article className="likes-custom-preset" key={slot} aria-label={slotName(slot)}>
              <strong>{item?.name || slotName(slot)}</strong>
              {item ? (
                <>
                  <label className="likes-preset-name">
                    <span>{text('likes.name', { slotName: slotName(slot) })}</span>
                    <input
                      value={draft}
                      disabled={controlsDisabled}
                      onChange={(event) => {
                        operation.reset();
                        setLoaded(null);
                        setNames((previous) => ({ ...previous, [slot]: event.target.value }));
                      }}
                      onKeyDown={(event) => {
                        if (event.key === 'Enter' && !nameInvalid && draft !== item.name) {
                          event.preventDefault();
                          rename(draft);
                        }
                      }}
                    />
                  </label>
                  <div className="likes-custom-preset-actions">
                    <button
                      type="button"
                      disabled={controlsDisabled || nameInvalid || draft === item.name}
                      onClick={() => rename(draft)}
                    >
                      {text('likes.saveName')}
                    </button>
                    <button
                      type="button"
                      disabled={controlsDisabled || !draft}
                      onClick={() => {
                        setNames((previous) => ({ ...previous, [slot]: '' }));
                        rename('');
                      }}
                    >
                      {text('likes.clearName')}
                    </button>
                  </div>
                  {nameInvalid && (
                    <p role="alert">{text('likes.useUpTo20CharactersWithoutLine')}</p>
                  )}
                  <dl className="likes-preset-summary">
                    <dt>{text('likes.mode')}</dt>
                    <dd>{item.mode === 'quick' ? text('likes.quick') : text('likes.standard')}</dd>
                    <dt>{text('likes.character')}</dt>
                    <dd>
                      {describe(
                        item.loadout.role,
                        catalog!.roles.find((role) => role.id === item.loadout.role)?.name,
                      )}
                    </dd>
                    <dt>Harness</dt>
                    <dd>
                      {item.loadout.harness === null
                        ? text('likes.none2')
                        : describe(
                            item.loadout.harness,
                            catalog!.harnesses.find(
                              (harness) => harness.id === item.loadout.harness,
                            )?.name,
                          )}
                    </dd>
                  </dl>
                  <ol className="likes-preset-skills">
                    {item.loadout.skills.map((id) => (
                      <li key={id}>
                        {describe(id, catalog!.skills.find((skill) => skill.id === id)?.name)}
                      </li>
                    ))}
                  </ol>
                  {invalid && (
                    <p className="likes-warning">
                      {text('likes.thisPresetNeedsRepairEditTheLoadout')}
                    </p>
                  )}
                </>
              ) : (
                <span>{text('likes.emptySlot')}</span>
              )}
              <div className="likes-custom-preset-actions">
                <button
                  type="button"
                  disabled={!item || invalid || controlsDisabled}
                  onClick={() => item && load(item)}
                >
                  {text('likes.load', { slotName: slotName(slot) })}
                </button>
                <button
                  type="button"
                  disabled={!slots || !validSelection || controlsDisabled}
                  onClick={() => (item ? setOverwrite(item) : void run(makeIntent(slot, '0')))}
                >
                  {item
                    ? text('likes.overwrite', { slotName: slotName(slot) })
                    : text('likes.saveTo', { slotName: slotName(slot) })}
                </button>
              </div>
            </article>
          );
        })}
      </div>
      {uncertain && (
        <div className="likes-custom-preset-actions">
          <button type="button" disabled={unavailable} onClick={() => void operation.check()}>
            {text('likes.checkCurrentPresets')}
          </button>
          <button
            type="button"
            disabled={unavailable}
            onClick={() => operation.variables && void run(operation.variables)}
          >
            {text('likes.retrySavingCustomPresets')}
          </button>
        </div>
      )}
      {operation.isPending && <p role="status">{text('likes.saving')}</p>}
      {operation.isSuccess && <p role="status">{text('likes.presetSaved')}</p>}
      {operation.outcome === 'conflict' && (
        <p role="alert">{text('likes.customPresetsChangedElsewhereCheckTheRefreshed')}</p>
      )}
      {uncertain && <p role="alert">{text('likes.savingCouldNotBeConfirmedCheckCurrent')}</p>}
      {operation.outcome === 'failed' && (
        <p role="alert">{text('likes.thePresetWasNotSavedCheckYour')}</p>
      )}
      {!!operation.refreshError && (
        <p role="alert">{text('likes.presetsCouldNotBeRefreshedRetryLoading')}</p>
      )}
      {loaded !== null && (
        <p role="status">
          {text('likes.loadedYouHaveNotJoinedMatchmaking', { slotName: slotName(loaded) })}
        </p>
      )}
      <ConfirmDialog
        open={!!overwrite}
        title={text('likes.overwriteCustomPresets')}
        description={text('likes.thisReplacesWithYourCurrentLoadout', {
          value: overwrite ? slotName(overwrite.slot) : '',
        })}
        confirmLabel={text('likes.confirmOverwrite')}
        cancelLabel={text('likes.cancel')}
        busy={operation.isPending}
        onCancel={() => setOverwrite(null)}
        onConfirm={() => {
          if (overwrite) void run(makeIntent(overwrite.slot, overwrite.revision));
          setOverwrite(null);
        }}
      />
    </section>
  );
}
