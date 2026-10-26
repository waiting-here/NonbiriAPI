import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { clearStationSession } from '@shared/charityManagement';
import {
  ENDPOINT_TAGS,
  getEndpointTagRulesPage,
  saveEndpointTagRule,
  deleteEndpointTagRule,
  type EndpointTagRule,
} from '@shared/operations/endpoints';
import { operationKey, responseOutcomeUnknown } from '@shared/operations/api';
import type { ManagementRole } from '@shared/operations/managedUsers';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { useSearchState } from '@shared/operations/useSearchState';
import { isStewardPermissionDenied, isUnauthorized } from '@shared/query/http';
import { EmptyState, ErrorState, LoadingState } from './States';
import { StewardPermissionNotice } from './StewardPermissionNotice';
import { Button, DataTable, Field, FilterBar, Note, Segmented } from './ui';
import { endpointTagKeys } from './endpointTagLabels';

type RuleMutation =
  | { kind: 'save'; rule: EndpointTagRule; key: string }
  | { kind: 'delete'; pattern: string; key: string };

export function EndpointTagRules({
  role,
  account,
  canTag,
}: {
  role: ManagementRole;
  account: string;
  canTag: boolean;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [params, setParams] = useSearchState();
  const query = params.get('rule_q') ?? '';
  const [draft, setDraft] = useState(query);
  const [previousQuery, setPreviousQuery] = useState(query);
  if (previousQuery !== query) {
    setPreviousQuery(query);
    setDraft(query);
  }
  const pager = useUrlPagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'endpoint-tag-rules',
    scopeKey: account,
    resetKey: query,
    pageParam: 'rule_page',
    pageSizeParam: 'rule_page_size',
  });
  const station = role === 'admin' ? 'admin' : 'user';
  const rules = useQuery({
    queryKey: [
      station,
      'operations',
      'endpoint-tag-rules',
      account,
      query,
      pager.page,
      pager.pageSize,
    ],
    queryFn: ({ signal }) =>
      getEndpointTagRulesPage(role, query, pager.page, pager.pageSize, signal),
    retry: false,
  });
  const [editor, setEditor] = useState<{ rule: EndpointTagRule; editing: boolean } | null>(null);
  const mutation = useMutation({
    mutationFn: (action: RuleMutation) =>
      action.kind === 'save'
        ? saveEndpointTagRule(role, action.rule, action.key)
        : deleteEndpointTagRule(role, action.pattern, action.key),
    onSuccess: async (_result, action) => {
      setEditor((current) =>
        action.kind === 'save' || current?.rule.pattern === action.pattern ? null : current,
      );
      await Promise.all([
        client.invalidateQueries({
          queryKey: [station, 'operations', 'endpoint-tag-rules', account],
        }),
        client.invalidateQueries({ queryKey: [station, 'operations', 'endpoints', account] }),
      ]);
    },
  });
  const error = rules.error ?? mutation.error;
  useEffect(() => {
    if (isUnauthorized(error)) clearStationSession(client, role);
    else if (role === 'steward' && isStewardPermissionDenied(error))
      void client.invalidateQueries({ queryKey: ['user', 'session'] });
  }, [client, role, error]);
  const unknown = responseOutcomeUnknown(mutation.error);
  const busy = mutation.isPending || unknown;
  const label = (tag: string) =>
    endpointTagKeys[tag as keyof typeof endpointTagKeys]
      ? t(endpointTagKeys[tag as keyof typeof endpointTagKeys])
      : tag;
  const update = (change: Partial<EndpointTagRule>) =>
    setEditor((current) =>
      current ? { ...current, rule: { ...current.rule, ...change } } : current,
    );
  const changeQuery = (value: string) =>
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      if (value) next.set('rule_q', value);
      else next.delete('rule_q');
      next.set('rule_page', '1');
      return next;
    });

  return (
    <div className="nb-stack endpoint-rules">
      <p className="nb-muted">{t('management.endpoints.rules.description')}</p>
      <p className="nb-muted">{t('management.endpoints.rules.precedence')}</p>
      <FilterBar
        ariaLabel={t('management.endpoints.rules.search')}
        secondaryLabel={t('common.filter')}
        activeCount={Number(Boolean(query))}
        onSubmit={() => changeQuery(draft.trim())}
        search={
          <>
            <Field label={t('management.endpoints.rules.search')}>
              {(field) => (
                <input
                  {...field}
                  type="search"
                  value={draft}
                  placeholder="example.com"
                  onChange={(event) => setDraft(event.target.value)}
                />
              )}
            </Field>
            <Button type="submit">{t('common.applyFilter')}</Button>
          </>
        }
      />
      {canTag && !editor ? (
        <div className="nb-actions">
          <Button
            disabled={busy}
            onClick={() => {
              mutation.reset();
              setEditor({ editing: false, rule: { pattern: '', action: 'add', tags: [] } });
            }}
          >
            {t('management.endpoints.rules.add')}
          </Button>
        </div>
      ) : null}
      {editor && canTag ? (
        <form
          key={editor.editing ? editor.rule.pattern : 'new'}
          className="endpoint-rule-editor nb-stack"
          onSubmit={(event) => {
            event.preventDefault();
            mutation.mutate({
              kind: 'save',
              key: operationKey(),
              rule: {
                ...editor.rule,
                pattern: editor.rule.pattern.trim(),
                tags: editor.rule.action === 'remove' ? [] : editor.rule.tags,
              },
            });
          }}
        >
          <h3>
            {t(
              editor.editing ? 'management.endpoints.rules.edit' : 'management.endpoints.rules.add',
            )}
          </h3>
          <fieldset className="endpoint-rule-editor__fields nb-stack" disabled={busy}>
            <Field
              label={t('management.endpoints.rules.pattern')}
              help={t('management.endpoints.rules.patternHelp')}
            >
              {(field) => (
                <input
                  {...field}
                  type="text"
                  autoFocus
                  required
                  value={editor.rule.pattern}
                  readOnly={editor.editing}
                  placeholder="*.example.com"
                  autoCapitalize="none"
                  spellCheck={false}
                  onChange={(event) => update({ pattern: event.target.value })}
                />
              )}
            </Field>
            <Segmented
              label={t('management.endpoints.rules.action')}
              value={editor.rule.action}
              onChange={(action) => update({ action })}
              options={[
                { value: 'add', label: t('management.endpoints.rules.addTags') },
                { value: 'remove', label: t('management.endpoints.rules.clearTags') },
              ]}
            />
            {editor.rule.action === 'add' ? (
              <fieldset className="endpoint-rule-tags">
                <legend>{t('management.endpoints.rules.chooseTags')}</legend>
                <div className="endpoint-rule-tags__options">
                  {ENDPOINT_TAGS.map((tag) => (
                    <label className="checkbox-label" key={tag}>
                      <input
                        type="checkbox"
                        checked={editor.rule.tags.includes(tag)}
                        onChange={(event) =>
                          update({
                            tags: event.target.checked
                              ? [...editor.rule.tags, tag]
                              : editor.rule.tags.filter((value) => value !== tag),
                          })
                        }
                      />
                      <span>{label(tag)}</span>
                    </label>
                  ))}
                </div>
                {editor.rule.tags.includes('mainstream') ? (
                  <p className="nb-muted">{t('management.endpoints.rules.mainstreamHelp')}</p>
                ) : null}
              </fieldset>
            ) : (
              <Note>{t('management.endpoints.rules.clearHelp')}</Note>
            )}
            <div className="nb-actions">
              <Button type="submit" variant="primary">
                {t('management.endpoints.rules.save')}
              </Button>
              <Button
                type="button"
                onClick={() => {
                  setEditor(null);
                  mutation.reset();
                }}
              >
                {t('common.cancel')}
              </Button>
            </div>
          </fieldset>
        </form>
      ) : null}
      {isStewardPermissionDenied(mutation.error) ? (
        <StewardPermissionNotice />
      ) : mutation.error ? (
        <ErrorState
          error={mutation.error}
          onRetry={
            unknown && mutation.variables ? () => mutation.mutate(mutation.variables!) : undefined
          }
        />
      ) : null}
      {mutation.isSuccess ? <Note tone="ok">{t('management.endpoints.rules.saved')}</Note> : null}
      {rules.isPending ? (
        <LoadingState />
      ) : isStewardPermissionDenied(rules.error) ? (
        <StewardPermissionNotice />
      ) : rules.error ? (
        <ErrorState error={rules.error} onRetry={() => void rules.refetch()} />
      ) : rules.data.data.length === 0 ? (
        <EmptyState
          title={t('management.endpoints.rules.empty')}
          body={t('management.endpoints.rules.emptyBody')}
        />
      ) : (
        <DataTable
          caption={t('management.endpoints.rules.title')}
          rows={rules.data.data}
          rowKey={(rule) => rule.pattern}
          columns={[
            {
              key: 'pattern',
              header: t('management.endpoints.rules.pattern'),
              cell: 'title',
              render: (rule) => <code className="endpoint-management-url">{rule.pattern}</code>,
            },
            {
              key: 'action',
              header: t('management.endpoints.rules.action'),
              cell: 'meta',
              mobileLabel: t('management.endpoints.rules.action'),
              render: (rule) =>
                rule.action === 'remove' ? (
                  t('management.endpoints.rules.clearTags')
                ) : (
                  <div className="endpoint-tag-badges">
                    {rule.tags.map((tag) => (
                      <span className="nb-badge" key={tag}>
                        {label(tag)}
                      </span>
                    ))}
                  </div>
                ),
            },
            ...(canTag
              ? [
                  {
                    key: 'actions',
                    header: t('common.workflow.actions'),
                    cell: 'action' as const,
                    render: (rule: EndpointTagRule) => (
                      <div className="nb-actions">
                        <Button
                          disabled={busy}
                          onClick={() => {
                            mutation.reset();
                            setEditor({ editing: true, rule: { ...rule, tags: [...rule.tags] } });
                          }}
                        >
                          {t('management.endpoints.rules.edit')}
                        </Button>
                        <Button
                          disabled={busy}
                          onClick={() =>
                            mutation.mutate({
                              kind: 'delete',
                              pattern: rule.pattern,
                              key: operationKey(),
                            })
                          }
                        >
                          {t('management.endpoints.rules.delete')}
                        </Button>
                      </div>
                    ),
                  },
                ]
              : []),
          ]}
        />
      )}
      {rules.data && !rules.error ? (
        <PagePagination
          alwaysShowJump
          metadata={rules.data.pagination}
          requestedPage={pager.page}
          busy={rules.isFetching}
          onPageChange={pager.setPage}
          onPageSizeChange={pager.setPageSize}
        />
      ) : null}
    </div>
  );
}
