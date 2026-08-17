import { useState, type FormEvent } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  Pagination,
  ReadOnlyValue,
  StatusBadge,
} from '@shared/components/States';
import { apiFetch } from '@shared/query/http';
import { asRecord, hasControlCharacters, optionalText } from '@shared/query/normalize';
import { adminKeys, type AdminUser, useAdminUsers } from '../data';

function number(value: number): string {
  return value.toLocaleString();
}

function elevatedToken(payload: unknown): string | undefined {
  const record = asRecord(payload);
  if (!record) return undefined;
  for (const key of ['token', 'elevated_token', 'capability']) {
    const value = record[key];
    if (
      typeof value === 'string' &&
      value.length >= 8 &&
      value.length <= 512 &&
      !hasControlCharacters(value)
    ) {
      return value;
    }
  }
  return undefined;
}

function UserLimits({ user }: { user: AdminUser }) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [endpointLimit, setEndpointLimit] = useState(
    user.endpoint_limit === undefined ? '' : String(user.endpoint_limit),
  );
  const [rpmLimit, setRpmLimit] = useState(user.rpm_limit === undefined ? '' : String(user.rpm_limit));
  const [validationError, setValidationError] = useState('');
  const mutation = useMutation({
    mutationFn: (values: { endpoint_limit: number | null; rpm_limit: number | null }) =>
      apiFetch<AdminUser>(`/admin/api/users/${encodeURIComponent(user.id)}`, {
        method: 'PATCH',
        json: values,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: adminKeys.usersRoot });
    },
  });

  const parseLimit = (value: string): number | null | undefined => {
    if (!value.trim()) return null;
    if (!/^\d+$/.test(value.trim())) return undefined;
    const parsed = Number(value.trim());
    return Number.isSafeInteger(parsed) && parsed >= 0 ? parsed : undefined;
  };

  const save = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setValidationError('');
    const endpoint = parseLimit(endpointLimit);
    const rpm = parseLimit(rpmLimit);
    if (endpoint === undefined || rpm === undefined) {
      setValidationError(t('admin.users.limitInvalid'));
      return;
    }
    mutation.mutate({ endpoint_limit: endpoint, rpm_limit: rpm });
  };

  return (
    <form className="limit-form" onSubmit={save} noValidate>
      <div className="limit-fields">
        <label>
          <span>{t('admin.users.endpointLimit')}</span>
          <input
            type="number"
            min="0"
            step="1"
            value={endpointLimit}
            onChange={(event) => setEndpointLimit(event.target.value)}
            aria-label={`${t('admin.users.endpointLimit')} · ${user.username}`}
          />
        </label>
        <label>
          <span>{t('admin.users.rpmLimit')}</span>
          <input
            type="number"
            min="0"
            step="1"
            value={rpmLimit}
            onChange={(event) => setRpmLimit(event.target.value)}
            aria-label={`${t('admin.users.rpmLimit')} · ${user.username}`}
          />
        </label>
      </div>
      <small className="muted">{t('admin.users.limitHint')}</small>
      {validationError ? <p className="field-error" role="alert">{validationError}</p> : null}
      {mutation.error ? <ErrorState error={mutation.error} /> : null}
      {mutation.isSuccess ? <p className="inline-success" role="status">{t('admin.users.saved')}</p> : null}
      <div className="table-actions">
        <button type="submit" className="btn btn-quiet" disabled={mutation.isPending}>
          {mutation.isPending ? t('common.working') : t('admin.users.saveLimits')}
        </button>
        <button
          type="button"
          className="btn btn-link"
          onClick={() => {
            setEndpointLimit('');
            setRpmLimit('');
          }}
        >
          {t('admin.users.restoreDefault')}
        </button>
      </div>
    </form>
  );
}

export function UsersPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [page, setPage] = useState(1);
  const users = useAdminUsers(page);
  const [selectedUser, setSelectedUser] = useState<AdminUser | null>(null);
  const [action, setAction] = useState<'ban' | 'unban' | 'delete' | null>(null);
  const [banReason, setBanReason] = useState('');
  const [deletePassword, setDeletePassword] = useState('');
  const [deleteError, setDeleteError] = useState<unknown>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);

  const actionMutation = useMutation({
    mutationFn: async () => {
      if (!selectedUser || action === 'delete') return;
      if (action === 'ban') {
        await apiFetch<void>(`/admin/api/users/${encodeURIComponent(selectedUser.id)}/ban`, {
          method: 'POST',
          json: { reason: optionalText(banReason, 512) },
        });
      } else {
        await apiFetch<void>(`/admin/api/users/${encodeURIComponent(selectedUser.id)}/unban`, {
          method: 'POST',
        });
      }
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: adminKeys.usersRoot });
      closeDialog();
    },
  });

  function closeDialog() {
    setSelectedUser(null);
    setAction(null);
    setBanReason('');
    setDeletePassword('');
    setDeleteError(null);
    actionMutation.reset();
  }

  const deleteUser = async () => {
    if (!selectedUser) return;
    const password = deletePassword;
    if (!password) {
      setDeleteError(new Error(t('admin.users.deletePasswordRequired')));
      return;
    }
    setDeletePassword('');
    setDeleteError(null);
    setDeleteBusy(true);
    try {
      const elevation = await apiFetch<unknown>('/admin/api/auth/elevate', {
        method: 'POST',
        json: { password },
      });
      const token = elevatedToken(elevation);
      await apiFetch<void>(`/admin/api/users/${encodeURIComponent(selectedUser.id)}`, {
        method: 'DELETE',
        headers: token ? { 'X-Elevated-Token': token } : undefined,
      });
      await queryClient.invalidateQueries({ queryKey: adminKeys.usersRoot });
      closeDialog();
    } catch (error) {
      setDeleteError(error);
    } finally {
      setDeleteBusy(false);
    }
  };

  const openAction = (user: AdminUser, nextAction: 'ban' | 'unban' | 'delete') => {
    setSelectedUser(user);
    setAction(nextAction);
    setDeleteError(null);
    actionMutation.reset();
  };

  return (
    <div className="page">
      <PageHeader
        eyebrow={t('app.name')}
        title={t('admin.users.title')}
        description={t('admin.users.description')}
      />
      <Card>
        <div className="card-title-row">
          <h2>{t('admin.users.listTitle')}</h2>
          <span className="muted">{t('common.page', { page })}</span>
        </div>
        {users.isPending ? <LoadingState /> : users.error ? <ErrorState error={users.error} onRetry={() => void users.refetch()} /> : users.data.items.length === 0 ? (
          <EmptyState title={t('admin.users.empty')} body={t('admin.users.emptyBody')} />
        ) : (
          <>
            <div className="table-wrap">
              <table>
                <caption>{t('admin.users.listTitle')}</caption>
                <thead>
                  <tr>
                    <th scope="col">{t('admin.users.username')}</th>
                    <th scope="col">{t('admin.users.discordId')}</th>
                    <th scope="col">{t('admin.users.status')}</th>
                    <th scope="col">{t('admin.users.limits')}</th>
                    <th scope="col">{t('admin.users.usage')}</th>
                    <th scope="col">{t('admin.users.created')}</th>
                    <th scope="col">{t('admin.users.actions')}</th>
                  </tr>
                </thead>
                <tbody>
                  {users.data.items.map((user) => (
                    <tr key={user.id}>
                      <td>
                        <strong>{user.username}</strong>
                        {user.banned_reason ? <p className="table-note">{user.banned_reason}</p> : null}
                      </td>
                      <td><ReadOnlyValue value={user.discord_id} /></td>
                      <td>
                        <StatusBadge
                          active={!user.is_banned}
                          label={user.is_banned ? t('admin.users.banned') : t('admin.users.active')}
                        />
                      </td>
                      <td><UserLimits user={user} /></td>
                      <td>
                        <span className="table-note">{number(user.total_requests)} {t('admin.users.requests')}</span>
                        <span className="table-note">{number(user.total_unknown_usage_requests)} {t('admin.users.unknown')}</span>
                      </td>
                      <td><ReadOnlyValue value={user.created_at} /></td>
                      <td>
                        <div className="table-actions">
                          <button type="button" className="btn btn-quiet" onClick={() => openAction(user, user.is_banned ? 'unban' : 'ban')}>
                            {user.is_banned ? t('admin.users.unban') : t('admin.users.ban')}
                          </button>
                          <button type="button" className="btn btn-danger" onClick={() => openAction(user, 'delete')}>
                            {t('admin.users.delete')}
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <Pagination page={page} hasNext={users.data.hasNext} onChange={setPage} />
          </>
        )}
      </Card>

      <ConfirmDialog
        open={Boolean(selectedUser && action)}
        title={
          action === 'ban'
            ? t('admin.users.banTitle')
            : action === 'unban'
              ? t('admin.users.unbanTitle')
              : t('admin.users.deleteTitle')
        }
        description={
          action === 'ban'
            ? t('admin.users.banBody')
            : action === 'unban'
              ? t('admin.users.unbanBody')
              : t('admin.users.deleteBody')
        }
        confirmLabel={
          action === 'ban'
            ? t('admin.users.banConfirm')
            : action === 'unban'
              ? t('admin.users.unbanConfirm')
              : t('admin.users.deleteConfirm')
        }
        danger={action !== 'unban'}
        busy={action === 'delete' ? deleteBusy : actionMutation.isPending}
        onCancel={closeDialog}
        onConfirm={() => {
          if (action === 'delete') void deleteUser();
          else actionMutation.mutate();
        }}
      >
        {action === 'ban' ? (
          <label>
            <span>{t('admin.users.banReason')}</span>
            <textarea
              value={banReason}
              onChange={(event) => setBanReason(event.target.value)}
              placeholder={t('admin.users.banReasonPlaceholder')}
              maxLength={512}
            />
          </label>
        ) : null}
        {action === 'delete' ? (
          <label>
            <span>{t('admin.users.elevatedPassword')}</span>
            <input
              type="password"
              value={deletePassword}
              onChange={(event) => setDeletePassword(event.target.value)}
              autoComplete="current-password"
              maxLength={512}
              aria-describedby="admin-delete-password-hint"
            />
            <small id="admin-delete-password-hint" className="muted">
              {t('admin.users.elevatedPasswordHint')}
            </small>
          </label>
        ) : null}
        {actionMutation.error ? <ErrorState error={actionMutation.error} /> : null}
        {deleteError ? <ErrorState error={deleteError} /> : null}
      </ConfirmDialog>
    </div>
  );
}
