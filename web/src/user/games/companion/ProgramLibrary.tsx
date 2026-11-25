import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { useState } from 'react';
import { useCompanionCopy } from '@shared/companionCopy';
import { ErrorState } from '@shared/components/States';
import { Field, Fields, Fold, FormActions } from '@shared/components/ui';
import {
  companionPath,
  type CompanionGame,
  type CompanionPrograms,
  type ProgramVersion,
} from '@shared/companion';
import { useCompanionRead, useCompanionWrite } from './api';

export function ProgramLibrary({
  game,
  programs,
  refresh,
  admin = false,
}: {
  game: CompanionGame;
  programs: CompanionPrograms;
  refresh: () => Promise<unknown>;
  admin?: boolean;
}) {
  const key = useCompanionCopy();
  const [deleting, setDeleting] = useState<string | null>(null);
  const [name, setName] = useState('');
  const [source, setSource] = useState('');
  const [fileError, setFileError] = useState('');
  const [view, setView] = useState<{ name: string; version: number } | null>(null);
  const path = view
    ? '/programs?name=' + encodeURIComponent(view.name) + '&version=' + view.version
    : '';
  const sourceQuery = useCompanionRead<{ source: string }>(game, path, { enabled: !!view });
  const save = useCompanionWrite<ProgramVersion | CompanionPrograms>(game, refresh, admin);
  const unknown = save.outcome === 'unknown';
  const busy = save.isPending || unknown;
  const official = programs.official;
  const [version, setVersion] = useState(official.version);
  const groups = admin ? [official] : programs.programs;
  const submit = () =>
    save.mutate({
      path: admin ? '/versions' : '/programs',
      body: admin ? { source } : { name: name.trim(), source },
    });
  return (
    <div className="nb-stack">
      {!admin && (
        <a href={companionPath(game) + '/kit'} download>
          {key('kit')}
        </a>
      )}
      <form
        className="nb-stack"
        onSubmit={(event) => {
          event.preventDefault();
          submit();
        }}
      >
        <Fields columns={1}>
          {!admin && (
            <Field label={key('programName')} help={key('programNameHelp')}>
              {(field) => (
                <input
                  {...field}
                  list="companion-program-names"
                  required
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                />
              )}
            </Field>
          )}
          {!admin && (
            <datalist id="companion-program-names">
              {groups.map((p) => (
                <option key={p.name} value={p.name} />
              ))}
            </datalist>
          )}
          <Field label={key('sourceFile')} help={key('sourceLimit')}>
            {(field) => (
              <input
                {...field}
                type="file"
                accept=".cpp,.cc,.cxx,text/plain"
                onChange={async (event) => {
                  const file = event.target.files?.[0];
                  if (!file) return;
                  if (file.size > 256 * 1024) {
                    setFileError(key('sourceLimit'));
                    return;
                  }
                  try {
                    setSource(await file.text());
                    setFileError('');
                  } catch {
                    setFileError(key('fileFailed'));
                  }
                }}
              />
            )}
          </Field>
          <Field label={key('source')} error={fileError}>
            {(field) => (
              <textarea
                {...field}
                className="mono"
                spellCheck={false}
                rows={5}
                required
                value={source}
                onChange={(e) => {
                  setSource(e.target.value);
                  setFileError('');
                }}
              />
            )}
          </Field>
        </Fields>
        <FormActions align="start">
          <button type="submit" className="nb-btn nb-btn--secondary" disabled={busy}>
            {key('compile')}
          </button>
        </FormActions>
      </form>
      {save.error && (
        <ErrorState
          error={save.error}
          onRetry={save.variables ? () => save.mutate(save.variables!) : undefined}
        />
      )}
      {save.data && 'status' in save.data && (
        <p role="status">
          {key(save.data.status === 'failed' ? 'failed' : 'ready')} · v{save.data.version}
        </p>
      )}
      {admin && (
        <>
          <Field label={key('officialVersion')}>
            {(field) => (
              <select
                {...field}
                value={version}
                onChange={(e) => setVersion(Number(e.target.value))}
              >
                {(official.versions?.length
                  ? official.versions
                  : [{ version: official.version, status: 'ready' }]
                ).map((v) => (
                  <option key={v.version} value={v.version} disabled={v.status !== 'ready'}>
                    v{v.version}
                  </option>
                ))}
              </select>
            )}
          </Field>
          <FormActions align="start">
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              disabled={busy || version === official.version}
              onClick={() => save.mutate({ path: '/activate', body: { version } })}
            >
              {key('activate')}
            </button>
          </FormActions>
        </>
      )}
      {groups.map((group) => (
        <section key={group.name} className="nb-stack nb-stack--sm">
          <div className="duel-actions">
            <h3>{admin ? key('official') : group.name}</h3>
            {!admin && (
              <button
                className="nb-btn nb-btn--danger-outline nb-btn--sm"
                type="button"
                disabled={busy}
                onClick={() => setDeleting(group.name)}
              >
                {key('delete')}
              </button>
            )}
          </div>
          {(group.versions ?? []).map((v) => (
            <div key={v.version} className="nb-stack nb-stack--sm">
              <div className="duel-actions">
                <strong>v{v.version}</strong>
                <span>
                  {key(
                    v.status === 'failed'
                      ? 'failed'
                      : v.version === group.version
                        ? 'active'
                        : 'ready',
                  )}
                </span>
                {!admin && (
                  <button
                    type="button"
                    className="nb-btn nb-btn--ghost nb-btn--sm"
                    onClick={() => setView({ name: group.name, version: v.version })}
                  >
                    {key('viewSource')}
                  </button>
                )}
                {!admin && v.status === 'ready' && v.version !== group.version && (
                  <button
                    type="button"
                    className="nb-btn nb-btn--secondary nb-btn--sm"
                    disabled={busy}
                    onClick={() =>
                      save.mutate({
                        path: '/programs/activate',
                        body: { version: v.version, name: group.name },
                      })
                    }
                  >
                    {key('activate')}
                  </button>
                )}
              </div>
              {v.diagnostic && (
                <Fold title={key('diagnostic')}>
                  <pre className="companion-source">{v.diagnostic}</pre>
                </Fold>
              )}
              {view?.name === group.name && view.version === v.version && (
                <div>
                  {sourceQuery.error ? (
                    <ErrorState
                      error={sourceQuery.error}
                      onRetry={() => void sourceQuery.refetch()}
                    />
                  ) : (
                    <pre className="companion-source">{sourceQuery.data?.source}</pre>
                  )}
                  <button
                    className="nb-btn nb-btn--ghost"
                    type="button"
                    onClick={() => setView(null)}
                  >
                    {key('closeSource')}
                  </button>
                </div>
              )}
            </div>
          ))}
        </section>
      ))}
      <ConfirmDialog
        open={!!deleting}
        title={key('deleteTitle', { name: deleting ?? '' })}
        description={key('deleteHelp')}
        confirmLabel={key('delete')}
        cancelLabel={key('cancel')}
        danger
        busy={busy}
        onCancel={() => setDeleting(null)}
        onConfirm={() => {
          if (deleting)
            save.mutate(
              { path: '/programs/delete', body: { name: deleting } },
              { onSuccess: () => setDeleting(null) },
            );
        }}
      />
    </div>
  );
}
