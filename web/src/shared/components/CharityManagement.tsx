import { useCallback, useEffect, useId, useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { Card, EmptyState, ErrorState, LoadingState, StatusBadge } from '@shared/components/States';
import { CursorPagination } from '@shared/operations/CursorPagination';
import { useCursorPager } from '@shared/operations/useCursorPager';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { formatDateTime } from '@shared/utils/datetime';
import {
  addManagedBindings,
  charityKeys,
  createManagedCharityModel,
  deleteManagedBinding,
  deleteManagedCharityModel,
  getManagedBindingCandidates,
  getManagedBindings,
  getManagedCharityModels,
  getManagedDonation,
  getManagedDonations,
  orderManagedBindings,
  patchManagedCharityModel,
  patchManagedDonationKey,
  reviewManagedDonation,
  type AdminDonation,
  type CharityModel,
  type CharityRole,
  type ManagedDonationKey,
  type StewardDonation,
  type TokenPrices,
} from '@shared/operations/charity';
import { useRetainedOperation } from '../../admin/features/operations/useRetainedOperation';
import '@shared/operations/operations.css';

type ManagedDonation = AdminDonation | StewardDonation;

const MAX_MONEY_MILLI = 9_000_000_000_000_000n;
const MAX_TOKEN_RESERVE = 2_147_483_647;
const MAX_UNIX_SECOND = 253_402_300_799;
const CANONICAL_DECIMAL = /^(0|[1-9][0-9]*)$/;
const CANONICAL_AMOUNT = /^(0|[1-9][0-9]*)(?:\.([0-9]{0,2}[1-9]))?$/;

function hasForbiddenControl(value: string): boolean {
  return Array.from(value).some((character) => {
    const point = character.codePointAt(0) ?? 0;
    return point < 0x20 || (point >= 0x7f && point <= 0x9f);
  });
}

function validText(value: string, maximum: number, required = false): boolean {
  return (!required || value.trim().length > 0)
    && Array.from(value).length <= maximum
    && !hasForbiddenControl(value);
}

function validCount(value: string | null): boolean {
  if (value === null) return true;
  if (!CANONICAL_DECIMAL.test(value)) return false;
  try {
    return BigInt(value) <= MAX_MONEY_MILLI;
  } catch {
    return false;
  }
}

function validAmount(value: string | null): boolean {
  if (value === null) return true;
  const match = CANONICAL_AMOUNT.exec(value);
  if (!match) return false;
  try {
    const whole = BigInt(match[1]);
    const fraction = BigInt((match[2] ?? '').padEnd(3, '0') || '0');
    return whole * 1_000n + fraction <= MAX_MONEY_MILLI;
  } catch {
    return false;
  }
}

function validTokenReserve(value: number): boolean {
  return Number.isSafeInteger(value) && value >= 0 && value <= MAX_TOKEN_RESERVE;
}

function NullableValue({
  label,
  value,
  onChange,
}: {
  label: string;
  value: string | null;
  onChange: (value: string | null) => void;
}) {
  const valueId = useId();
  const unlimitedId = useId();
  const [unlimited, setUnlimited] = useState(value === null);
  return (
    <div>
      <label htmlFor={valueId}>{label}</label>
      <input
        id={valueId}
        value={value ?? ''}
        disabled={unlimited}
        onChange={(event) => onChange(event.target.value)}
      />
      <small>
        <label htmlFor={unlimitedId}>
        <input
          id={unlimitedId}
          type="checkbox"
          checked={unlimited}
          onChange={(event) => {
            setUnlimited(event.target.checked);
            onChange(event.target.checked ? null : '0');
          }}
        />{' '}
          Unlimited {label.toLowerCase()}
        </label>
      </small>
    </div>
  );
}

interface KeySettingsDraft {
  price_limit: string | null;
  calls_limit: string | null;
  tokens_limit: string | null;
  token_reserve: number;
  enabled: boolean;
  safe_note: string;
}
interface KeyManagementDraft extends Omit<KeySettingsDraft, 'enabled'> {
  enabled: boolean | null;
}

const keySettingsDraft = (key: ManagedDonationKey): KeySettingsDraft => ({
  price_limit: key.limits.price,
  calls_limit: key.limits.calls,
  tokens_limit: key.limits.tokens,
  token_reserve: key.token_reserve,
  enabled:
    key.charity_state !== 'disabled' &&
    key.charity_state !== 'ended' &&
    key.charity_state !== 'expired',
  safe_note: key.safe_note,
});

const keyManagementDraft = (key: ManagedDonationKey): KeyManagementDraft => ({
  ...keySettingsDraft(key),
  enabled: null,
});

function keySettingsError(value: Omit<KeySettingsDraft, 'enabled'>): string | null {
  if (!validAmount(value.price_limit)) {
    return 'Price limit must be an exact non-negative credit amount with at most three decimals.';
  }
  if (!validCount(value.calls_limit) || !validCount(value.tokens_limit)) {
    return 'Call and token limits must be canonical integers no greater than 9,000,000,000,000,000.';
  }
  if (!validTokenReserve(value.token_reserve)) {
    return 'Token reserve must be an integer from 0 through 2,147,483,647.';
  }
  if (!validText(value.safe_note, 256)) {
    return 'Reviewer-safe note must be at most 256 characters and contain no control characters.';
  }
  return null;
}

function DonationKeyEditor({
  item,
  donation,
  role,
  refresh,
  onCapabilityLoss,
}: {
  item: ManagedDonationKey;
  donation: ManagedDonation;
  role: CharityRole;
  refresh: () => Promise<unknown>;
  onCapabilityLoss?: () => void;
}) {
  const [draft, setDraft] = useState(() => keyManagementDraft(item));
  const [reset, setReset] = useState(false);
  const save = useRetainedOperation<
    KeyManagementDraft & { reset_failure_streak: boolean },
    ManagedDonation
  >(
    (input: KeyManagementDraft & { reset_failure_streak: boolean }, key) =>
      patchManagedDonationKey(
        role,
        donation.id,
        item.id,
        {
          expected_revision: donation.revision,
          ...(input.enabled === null ? {} : { enabled: input.enabled }),
          price_limit: input.price_limit,
          calls_limit: input.calls_limit,
          tokens_limit: input.tokens_limit,
          token_reserve: input.token_reserve,
          safe_note: input.safe_note,
          ...(input.reset_failure_streak ? { reset_failure_streak: true } : {}),
        },
        key,
      ),
    refresh,
    charityKeys.root(role),
  );
  const validationError = keySettingsError(draft);
  const capabilityLost = isUnauthorized(save.error) || isForbidden(save.error);
  useEffect(() => {
    if (capabilityLost) onCapabilityLoss?.();
  }, [capabilityLost, onCapabilityLoss]);
  if (capabilityLost) {
    return <p className="field-error" role="alert">Charity management access is no longer available.</p>;
  }
  return (
    <section className="ops-subcard">
      <h4>
        Key {item.id} · {item.display_head}…{item.display_tail}
      </h4>
      <p>
        {item.safe_source.connector_type} · {item.safe_source.base_url}
      </p>
      <div className="ops-toolbar">
        <StatusBadge
          active={item.charity_state === 'available'}
          danger={item.streak.failure_disabled}
          label={item.charity_state}
        />
        <span>physical {item.physical_enabled ? 'enabled' : 'disabled'}</span>
        <span>streak {item.streak.count}</span>
      </div>
      <dl className="ops-kv">
        <dt>Price</dt>
        <dd>
          {item.usage.price_used} used + {item.usage.price_inflight} in flight /{' '}
          {item.limits.price ?? 'unlimited'}
        </dd>
        <dt>Calls</dt>
        <dd>
          {item.usage.calls_used} + {item.usage.calls_inflight} / {item.limits.calls ?? 'unlimited'}
        </dd>
        <dt>Tokens</dt>
        <dd>
          {item.usage.tokens_used} + {item.usage.tokens_inflight} /{' '}
          {item.limits.tokens ?? 'unlimited'}
        </dd>
        {item.ended_reason ? (
          <>
            <dt>Ended</dt>
            <dd>{item.ended_reason}</dd>
          </>
        ) : null}
      </dl>
      {donation.status === 'approved' ? (
        <>
          <div className="ops-field-grid">
            <NullableValue
              label="Price limit"
              value={draft.price_limit}
              onChange={(value) => setDraft({ ...draft, price_limit: value })}
            />
            <NullableValue
              label="Call limit"
              value={draft.calls_limit}
              onChange={(value) => setDraft({ ...draft, calls_limit: value })}
            />
            <NullableValue
              label="Token limit"
              value={draft.tokens_limit}
              onChange={(value) => setDraft({ ...draft, tokens_limit: value })}
            />
            <label>
              <span>Token reserve</span>
              <input
                type="number"
                min="0"
                max={MAX_TOKEN_RESERVE}
                step="1"
                value={draft.token_reserve}
                onChange={(event) =>
                  setDraft({ ...draft, token_reserve: Number(event.target.value) })
                }
              />
            </label>
            <label>
              <span>Reviewer-safe note</span>
              <input
                value={draft.safe_note}
                onChange={(event) => setDraft({ ...draft, safe_note: event.target.value })}
              />
            </label>
            <label>
              <span>Charity switch change</span>
              <select
                value={draft.enabled === null ? '' : String(draft.enabled)}
                onChange={(event) =>
                  setDraft({
                    ...draft,
                    enabled:
                      event.target.value === '' ? null : event.target.value === 'true',
                  })
                }
              >
                <option value="">Leave unchanged</option>
                <option value="true">Enable for charity</option>
                <option value="false">Disable for charity</option>
              </select>
            </label>
            {item.streak.failure_disabled ? (
              <label>
                <input
                  type="checkbox"
                  checked={reset}
                  onChange={(event) => setReset(event.target.checked)}
                />{' '}
                Reset failure streak
              </label>
            ) : null}
          </div>
          {validationError ? <p className="field-error" role="alert">{validationError}</p> : null}
          {save.error ? <ErrorState error={save.error} /> : null}
          <button
            className="btn btn-secondary"
            type="button"
            disabled={save.isPending || Boolean(validationError)}
            onClick={() => save.mutate({ ...draft, reset_failure_streak: reset })}
          >
            Save key controls
          </button>
        </>
      ) : (
        <p className="muted">Key limits become editable after approval.</p>
      )}
    </section>
  );
}

function DonationDetail({
  item,
  role,
  refresh,
  onCapabilityLoss,
}: {
  item: ManagedDonation;
  role: CharityRole;
  refresh: () => Promise<unknown>;
  onCapabilityLoss?: () => void;
}) {
  const [decision, setDecision] = useState<'approve' | 'reject'>('approve');
  const [reason, setReason] = useState('');
  const [expires, setExpires] = useState(() => dateTimeDraft(item.expires_at).value);
  const [noExpiry, setNoExpiry] = useState(item.expires_at === null);
  const [confirmed, setConfirmed] = useState(false);
  const [keys, setKeys] = useState<Record<string, KeySettingsDraft>>(() =>
    Object.fromEntries(item.keys.map((key) => [key.id, keySettingsDraft(key)])),
  );
  const review = useRetainedOperation<
    {
      decision: 'approve' | 'reject';
      reason: string;
      expires_at: number | null;
      settings: Record<string, KeySettingsDraft>;
    },
    ManagedDonation
  >(
    (
      input: {
        decision: 'approve' | 'reject';
        reason: string;
        expires_at: number | null;
        settings: Record<string, KeySettingsDraft>;
      },
      key,
    ) =>
      reviewManagedDonation(
        role,
        item.id,
        input.decision === 'reject'
          ? { decision: 'reject', expected_revision: item.revision, reason: input.reason }
          : {
              decision: 'approve',
              expected_revision: item.revision,
              reason: input.reason,
              expires_at: input.expires_at,
              key_settings: item.keys.map((entry) => ({
                donation_key_id: entry.id,
                ...input.settings[entry.id],
              })),
            },
        key,
      ),
    refresh,
    charityKeys.root(role),
  );
  const owner = item.owner ?? { user_id: 'deidentified', display_name: 'Deidentified' };
  const expiry = expires ? Math.floor(Date.parse(expires) / 1_000) : NaN;
  let validationError: string | null = null;
  if (!validText(reason.trim(), 1_024, true)) {
    validationError = 'Review reason is required and must be at most 1,024 characters.';
  } else if (decision === 'approve') {
    if (!noExpiry && (!Number.isSafeInteger(expiry) || expiry < 0 || expiry > MAX_UNIX_SECOND)) {
      validationError = 'Whole-donation expiry must be a valid supported date.';
    } else if (item.keys.length === 0 || item.keys.some((entry) => !keys[entry.id])) {
      validationError = 'Approval requires a complete setting for every surviving key.';
    } else {
      validationError = item.keys
        .map((entry) => keySettingsError(keys[entry.id]))
        .find((error): error is string => error !== null) ?? null;
    }
  }
  const capabilityLost = isUnauthorized(review.error) || isForbidden(review.error);
  useEffect(() => {
    if (capabilityLost) onCapabilityLoss?.();
  }, [capabilityLost, onCapabilityLoss]);
  if (capabilityLost) {
    return <Card><p className="field-error" role="alert">Charity management access is no longer available.</p></Card>;
  }
  return (
    <div className="ops-stack">
      <Card>
        <h3>Donation {item.id}</h3>
        <div className="ops-toolbar">
          <StatusBadge
            active={item.status === 'approved'}
            danger={
              item.status === 'rejected' || item.status === 'deleted' || item.status === 'expired'
            }
            label={item.status}
          />
          <span>revision {item.revision}</span>
        </div>
        <dl className="ops-kv">
          <dt>Owner</dt>
          <dd>
            {owner.display_name} · {owner.user_id}
            {role === 'admin' && 'discord_id' in owner
              ? ` · ${owner.discord_id ?? 'Discord detached'}`
              : ''}
          </dd>
          <dt>Donor description</dt>
          <dd>{item.description || 'No description'}</dd>
          <dt>Created / updated</dt>
          <dd>
            {formatDateTime(item.created_at)} / {formatDateTime(item.updated_at)}
          </dd>
          {item.expires_at !== null ? (
            <>
              <dt>Expires</dt>
              <dd>{formatDateTime(item.expires_at)}</dd>
            </>
          ) : null}
          {item.review_result ? (
            <>
              <dt>Review</dt>
              <dd>
                {item.review_result.decision} · {item.review_result.reason} ·{' '}
                {formatDateTime(item.review_result.reviewed_at)}
              </dd>
            </>
          ) : null}
          <dt>Reviewer</dt>
          <dd>
            {item.reviewer
              ? `${item.reviewer.role} · ${item.reviewer.user_id ?? 'deidentified'}`
              : 'not reviewed'}
          </dd>
        </dl>
      </Card>
      {item.status === 'pending' ? (
        <Card>
          <h3>Review pending submission</h3>
          <p>
            Approval requires a complete setting for every currently surviving key. The donor
            description, physical source, and endpoint ownership cannot be edited here.
          </p>
          <div className="ops-field-grid">
            <label>
              <span>Decision</span>
              <select
                value={decision}
                onChange={(event) => {
                  setDecision(event.target.value as typeof decision);
                  setConfirmed(false);
                }}
              >
                <option value="approve">Approve</option>
                <option value="reject">Reject</option>
              </select>
            </label>
            <label>
              <span>Reason</span>
              <input
                value={reason}
                onChange={(event) => setReason(event.target.value)}
              />
            </label>
            {decision === 'approve' ? (
              <>
                <label>
                  <span>Whole-donation expiry</span>
                  <input
                    type="datetime-local"
                    step="1"
                    value={expires}
                    disabled={noExpiry}
                    onChange={(event) => setExpires(event.target.value)}
                  />
                </label>
                <label>
                  <input
                    type="checkbox"
                    checked={noExpiry}
                    onChange={(event) => setNoExpiry(event.target.checked)}
                  />{' '}
                  No expiry
                </label>
              </>
            ) : null}
          </div>
          {decision === 'approve' ? (
            <div className="ops-stack">
              {item.keys.map((entry) => {
                const draft = keys[entry.id];
                return (
                  <section key={entry.id} className="ops-subcard">
                    <h4>
                      {entry.display_head}…{entry.display_tail} · {entry.safe_source.base_url}
                    </h4>
                    <div className="ops-field-grid">
                      <NullableValue
                        label="Price limit"
                        value={draft.price_limit}
                        onChange={(value) =>
                          setKeys({ ...keys, [entry.id]: { ...draft, price_limit: value } })
                        }
                      />
                      <NullableValue
                        label="Call limit"
                        value={draft.calls_limit}
                        onChange={(value) =>
                          setKeys({ ...keys, [entry.id]: { ...draft, calls_limit: value } })
                        }
                      />
                      <NullableValue
                        label="Token limit"
                        value={draft.tokens_limit}
                        onChange={(value) =>
                          setKeys({ ...keys, [entry.id]: { ...draft, tokens_limit: value } })
                        }
                      />
                      <label>
                        <span>Token reserve</span>
                        <input
                          type="number"
                          min="0"
                          max={MAX_TOKEN_RESERVE}
                          step="1"
                          value={draft.token_reserve}
                          onChange={(event) =>
                            setKeys({
                              ...keys,
                              [entry.id]: { ...draft, token_reserve: Number(event.target.value) },
                            })
                          }
                        />
                      </label>
                      <label>
                        <span>Reviewer-safe note</span>
                        <input
                          value={draft.safe_note}
                          onChange={(event) =>
                            setKeys({
                              ...keys,
                              [entry.id]: { ...draft, safe_note: event.target.value },
                            })
                          }
                        />
                      </label>
                      <label>
                        <input
                          type="checkbox"
                          checked={draft.enabled}
                          onChange={(event) =>
                            setKeys({
                              ...keys,
                              [entry.id]: { ...draft, enabled: event.target.checked },
                            })
                          }
                        />{' '}
                        Enable for charity
                      </label>
                    </div>
                  </section>
                );
              })}
            </div>
          ) : null}
          <label>
            <input
              type="checkbox"
              checked={confirmed}
              onChange={(event) => setConfirmed(event.target.checked)}
            />{' '}
            I confirm this review result and its whole-donation consequences.
          </label>
          {validationError && (reason.length > 0 || confirmed) ? (
            <p className="field-error" role="alert">{validationError}</p>
          ) : null}
          {review.error ? <ErrorState error={review.error} /> : null}
          <button
            className={decision === 'reject' ? 'btn btn-danger' : 'btn btn-primary'}
            type="button"
            disabled={
              !reason.trim() ||
              !confirmed ||
              review.isPending ||
              Boolean(validationError)
            }
            onClick={() =>
              review.mutate({
                decision,
                reason: reason.trim(),
                expires_at: decision === 'approve' && !noExpiry ? expiry : null,
                settings: keys,
              })
            }
          >
            {decision === 'approve' ? 'Approve donation' : 'Reject donation'}
          </button>
        </Card>
      ) : null}
      <Card>
        <h3>Donation keys</h3>
        <div className="ops-stack">
          {item.keys.map((key) => (
            <DonationKeyEditor
              key={`${key.id}:${item.revision}`}
              item={key}
              donation={item}
              role={role}
              refresh={refresh}
              onCapabilityLoss={onCapabilityLoss}
            />
          ))}
        </div>
      </Card>
    </div>
  );
}

function DonationsPanel({
  role,
  onCapabilityLoss,
}: {
  role: CharityRole;
  onCapabilityLoss?: () => void;
}) {
  const pager = useCursorPager();
  const [status, setStatus] = useState('');
  const [selected, setSelected] = useState('');
  const list = useQuery({
    queryKey: charityKeys.donations(role, status, pager.cursor),
    queryFn: () => getManagedDonations(role, status, pager.cursor),
    retry: false,
  });
  const detail = useQuery({
    queryKey: charityKeys.donation(role, selected),
    queryFn: () => getManagedDonation(role, selected),
    enabled: Boolean(selected),
    retry: false,
  });
  const capabilityLost = isUnauthorized(list.error) || isForbidden(list.error)
    || isUnauthorized(detail.error) || isForbidden(detail.error);
  useEffect(() => {
    if (capabilityLost) onCapabilityLoss?.();
  }, [capabilityLost, onCapabilityLoss]);
  const refresh = async () => {
    await Promise.all([list.refetch(), selected ? detail.refetch() : Promise.resolve()]);
  };
  if (capabilityLost) {
    return <Card><p className="field-error" role="alert">Charity management access is no longer available.</p></Card>;
  }
  return (
    <div className="ops-stack">
      <Card>
        <div className="ops-toolbar">
          <label>
            <span>Status</span>
            <select
              value={status}
              onChange={(event) => {
                pager.reset();
                setSelected('');
                setStatus(event.target.value);
              }}
            >
              <option value="">All</option>
              {['pending', 'approved', 'rejected', 'deleted', 'expired'].map((value) => (
                <option key={value}>{value}</option>
              ))}
            </select>
          </label>
        </div>
        {list.isPending ? (
          <LoadingState />
        ) : list.error ? (
          <ErrorState error={list.error} onRetry={() => void list.refetch()} />
        ) : list.data.data.length === 0 ? (
          <EmptyState
            title="No donations"
            body="No surviving donation matches this role-safe filter."
          />
        ) : (
          <>
            <div className="ops-table-scroll">
              <table className="ops-table">
                <thead>
                  <tr>
                    <th>Donation</th>
                    <th>Owner</th>
                    <th>Status</th>
                    <th>Keys</th>
                    <th>Open</th>
                  </tr>
                </thead>
                <tbody>
                  {list.data.data.map((item) => (
                    <tr key={item.id}>
                      <td>
                        {item.id}
                        <small>{item.description}</small>
                      </td>
                      <td>{item.owner?.display_name ?? 'Deidentified'}</td>
                      <td>
                        <StatusBadge active={item.status === 'approved'} label={item.status} />
                      </td>
                      <td>{item.keys.length}</td>
                      <td>
                        <button
                          className="btn btn-secondary"
                          type="button"
                          onClick={() => setSelected(item.id)}
                        >
                          Review
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <CursorPagination
              page={pager.page}
              nextCursor={list.data.next_cursor}
              onPrevious={pager.previous}
              onNext={pager.next}
            />
          </>
        )}
      </Card>
      {selected ? (
        detail.isPending ? (
          <LoadingState />
        ) : detail.error ? (
          <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
        ) : (
          <DonationDetail
            key={`${detail.data.id}:${detail.data.revision}`}
            item={detail.data}
            role={role}
            refresh={refresh}
            onCapabilityLoss={onCapabilityLoss}
          />
        )
      ) : null}
    </div>
  );
}

interface DateTimeDraft {
  value: string;
  original: number | null;
  dirty: boolean;
}

interface ModelDraft {
  provider: string;
  model: string;
  enabled: boolean;
  mode: 'per_request' | 'per_token';
  requestUser: string;
  requestDonor: string;
  userPrices: TokenPrices;
  donorRewards: TokenPrices;
  discountEnabled: boolean;
  discountPercent: number;
  discountStart: DateTimeDraft;
  discountEnd: DateTimeDraft;
  flatten: boolean;
}
const zeroPrices = (): TokenPrices => ({
  uncached_input: '0',
  cache_write_input: '0',
  cache_read_input: '0',
  output: '0',
});

const datePart = (value: number) => String(value).padStart(2, '0');
function dateTimeDraft(epoch: number | null | undefined): DateTimeDraft {
  if (epoch === null || epoch === undefined) return { value: '', original: null, dirty: false };
  const date = new Date(epoch * 1_000);
  return {
    value: `${date.getFullYear()}-${datePart(date.getMonth() + 1)}-${datePart(date.getDate())}T${datePart(date.getHours())}:${datePart(date.getMinutes())}:${datePart(date.getSeconds())}`,
    original: epoch,
    dirty: false,
  };
}

function dateTimeEpoch(draft: DateTimeDraft): number | null {
  if (!draft.dirty) return draft.original;
  return draft.value ? Math.floor(Date.parse(draft.value) / 1_000) : null;
}

function modelDraft(model?: CharityModel): ModelDraft {
  return {
    provider: model?.provider ?? '',
    model: model?.model ?? '',
    enabled: model?.enabled ?? true,
    mode: model?.pricing.mode ?? 'per_request',
    requestUser: model?.pricing.mode === 'per_request' ? model.pricing.user_price : '0',
    requestDonor: model?.pricing.mode === 'per_request' ? model.pricing.donor_reward : '0',
    userPrices: model?.pricing.mode === 'per_token' ? model.pricing.user_prices : zeroPrices(),
    donorRewards: model?.pricing.mode === 'per_token' ? model.pricing.donor_rewards : zeroPrices(),
    discountEnabled: model?.discount.enabled ?? false,
    discountPercent: model?.discount.percent ?? 0,
    discountStart: dateTimeDraft(model?.discount.start_at),
    discountEnd: dateTimeDraft(model?.discount.end_at),
    flatten: model?.flatten_tool_calls ?? false,
  };
}
function modelBody(draft: ModelDraft) {
  return {
    provider: draft.provider.trim(),
    model: draft.model.trim(),
    enabled: draft.enabled,
    pricing:
      draft.mode === 'per_request'
        ? { mode: 'per_request', user_price: draft.requestUser, donor_reward: draft.requestDonor }
        : { mode: 'per_token', user_prices: draft.userPrices, donor_rewards: draft.donorRewards },
    discount: {
      enabled: draft.discountEnabled,
      percent: draft.discountPercent,
      start_at: dateTimeEpoch(draft.discountStart),
      end_at: dateTimeEpoch(draft.discountEnd),
    },
    flatten_tool_calls: draft.flatten,
  };
}

function modelDraftError(draft: ModelDraft): string | null {
  const provider = draft.provider.trim();
  const model = draft.model.trim();
  if (!validText(provider, 64, true) || !validText(model, 64, true) || provider.startsWith('[公益]')) {
    return 'Provider and model are required, limited to 64 characters, and cannot reuse the charity prefix.';
  }
  const prices = draft.mode === 'per_request'
    ? [draft.requestUser, draft.requestDonor]
    : [...Object.values(draft.userPrices), ...Object.values(draft.donorRewards)];
  if (prices.some((value) => !validAmount(value))) {
    return 'Every price must be an exact non-negative credit amount with at most three decimals.';
  }
  if (!Number.isInteger(draft.discountPercent)
    || draft.discountPercent < 0
    || draft.discountPercent > 100) {
    return 'Discount percent must be an integer from 0 through 100.';
  }
  const start = dateTimeEpoch(draft.discountStart);
  const end = dateTimeEpoch(draft.discountEnd);
  if (start !== null && (!Number.isSafeInteger(start) || start < 0 || start > MAX_UNIX_SECOND)
    || end !== null && (!Number.isSafeInteger(end) || end < 0 || end > MAX_UNIX_SECOND)
    || start !== null && end !== null && end <= start) {
    return 'Discount dates must be valid and the end must be later than the start.';
  }
  return null;
}

function ModelForm({
  role,
  model,
  refresh,
  onDeleted,
  onCapabilityLoss,
}: {
  role: CharityRole;
  model?: CharityModel;
  refresh: () => Promise<unknown>;
  onDeleted?: () => void;
  onCapabilityLoss?: () => void;
}) {
  const [draft, setDraft] = useState(() => modelDraft(model));
  const [confirmDelete, setConfirmDelete] = useState(false);
  const save = useRetainedOperation<ModelDraft, CharityModel>(
    (input: ModelDraft, key) =>
      model
        ? patchManagedCharityModel(
            role,
            model.id,
            { expected_revision: model.revision, ...modelBody(input) },
            key,
          )
        : createManagedCharityModel(role, modelBody(input), key),
    refresh,
    charityKeys.root(role),
  );
  const remove = useRetainedOperation<{ id: string; revision: string }, void>(
    (_input: { id: string; revision: string }, key) =>
      deleteManagedCharityModel(role, model!.id, model!.revision, key),
    refresh,
    charityKeys.root(role),
  );
  const validationError = modelDraftError(draft);
  const capabilityLost = isUnauthorized(save.error) || isForbidden(save.error)
    || isUnauthorized(remove.error) || isForbidden(remove.error);
  useEffect(() => {
    if (capabilityLost) onCapabilityLoss?.();
  }, [capabilityLost, onCapabilityLoss]);
  if (capabilityLost) {
    return <Card><p className="field-error" role="alert">Charity management access is no longer available.</p></Card>;
  }
  const setPrice = (side: 'userPrices' | 'donorRewards', field: keyof TokenPrices, value: string) =>
    setDraft({ ...draft, [side]: { ...draft[side], [field]: value } });
  return (
    <Card>
      <h3>{model ? model.full_name : 'Create charity model'}</h3>
      {model ? (
        <p>
          Revision {model.revision} · bindings {model.binding_count} · rolling success{' '}
          {model.rolling_success.percent ?? 'no sample'}%
        </p>
      ) : null}
      <div className="ops-field-grid">
        <label>
          <span>Provider</span>
          <input
            value={draft.provider}
            onChange={(event) => setDraft({ ...draft, provider: event.target.value })}
          />
        </label>
        <label>
          <span>Model</span>
          <input
            value={draft.model}
            onChange={(event) => setDraft({ ...draft, model: event.target.value })}
          />
        </label>
        <label>
          <span>Pricing mode</span>
          <select
            value={draft.mode}
            onChange={(event) =>
              setDraft({ ...draft, mode: event.target.value as ModelDraft['mode'] })
            }
          >
            <option value="per_request">Per request</option>
            <option value="per_token">Per token</option>
          </select>
        </label>
        <label>
          <input
            type="checkbox"
            checked={draft.enabled}
            onChange={(event) => setDraft({ ...draft, enabled: event.target.checked })}
          />{' '}
          Enabled for new claims
        </label>
        <label>
          <input
            type="checkbox"
            checked={draft.flatten}
            onChange={(event) => setDraft({ ...draft, flatten: event.target.checked })}
          />{' '}
          Flatten tool calls
        </label>
      </div>
      {draft.mode === 'per_request' ? (
        <div className="ops-field-grid">
          <label>
            <span>User price</span>
            <input
              value={draft.requestUser}
              onChange={(event) => setDraft({ ...draft, requestUser: event.target.value })}
            />
          </label>
          <label>
            <span>Donor reward</span>
            <input
              value={draft.requestDonor}
              onChange={(event) => setDraft({ ...draft, requestDonor: event.target.value })}
            />
          </label>
        </div>
      ) : (
        <div className="ops-grid">
          {(['userPrices', 'donorRewards'] as const).map((side) => (
            <section key={side} className="ops-subcard">
              <h4>{side === 'userPrices' ? 'User prices' : 'Donor rewards'}</h4>
              {(Object.keys(draft[side]) as (keyof TokenPrices)[]).map((field) => (
                <label key={field}>
                  <span>{field.replaceAll('_', ' ')}</span>
                  <input
                    value={draft[side][field]}
                    onChange={(event) => setPrice(side, field, event.target.value)}
                  />
                </label>
              ))}
            </section>
          ))}
        </div>
      )}
      <div className="ops-field-grid">
        <label>
          <input
            type="checkbox"
            checked={draft.discountEnabled}
            onChange={(event) => setDraft({ ...draft, discountEnabled: event.target.checked })}
          />{' '}
          Discount enabled
        </label>
        <label>
          <span>Discount percent</span>
          <input
            type="number"
            min="0"
            max="100"
            value={draft.discountPercent}
            onChange={(event) =>
              setDraft({ ...draft, discountPercent: Number(event.target.value) })
            }
          />
        </label>
        <label>
          <span>Discount start</span>
          <input
            type="datetime-local"
            step="1"
            value={draft.discountStart.value}
            onChange={(event) =>
              setDraft({
                ...draft,
                discountStart: { ...draft.discountStart, value: event.target.value, dirty: true },
              })
            }
          />
        </label>
        <label>
          <span>Discount end</span>
          <input
            type="datetime-local"
            step="1"
            value={draft.discountEnd.value}
            onChange={(event) =>
              setDraft({
                ...draft,
                discountEnd: { ...draft.discountEnd, value: event.target.value, dirty: true },
              })
            }
          />
        </label>
      </div>
      {save.error ? (
        <ErrorState error={save.error} />
      ) : remove.error ? (
        <ErrorState error={remove.error} />
      ) : null}
      {validationError ? <p className="field-error" role="alert">{validationError}</p> : null}
      <div className="ops-actions">
        <button
          className="btn btn-primary"
          type="button"
          disabled={Boolean(validationError) || save.isPending}
          onClick={() => save.mutate(draft)}
        >
          {model ? 'Save model' : 'Create model'}
        </button>
        {model ? (
          <button className="btn btn-danger" type="button" onClick={() => setConfirmDelete(true)}>
            Delete model
          </button>
        ) : null}
      </div>
      {confirmDelete && model ? (
        <ConfirmDialog
          open
          title="Delete charity model?"
          description="The logical model and its binding projection are removed. Existing accepted work keeps its frozen authority."
          confirmLabel="DELETE model"
          danger
          busy={remove.isPending}
          onCancel={() => setConfirmDelete(false)}
          onConfirm={() => {
            setConfirmDelete(false);
            remove.mutate({ id: model.id, revision: model.revision }, { onSuccess: onDeleted });
          }}
        />
      ) : null}
    </Card>
  );
}

function BindingsPanel({ role, model, onCapabilityLoss }: { role: CharityRole; model: CharityModel; onCapabilityLoss?: () => void }) {
  const pager = useCursorPager();
  const [queryDraft, setQueryDraft] = useState('');
  const [query, setQuery] = useState('');
  const [selected, setSelected] = useState<Record<string, boolean>>({});
  const bindings = useQuery({
    queryKey: charityKeys.bindings(role, model.id),
    queryFn: () => getManagedBindings(role, model.id),
    retry: false,
  });
  const candidates = useQuery({
    queryKey: charityKeys.candidates(role, model.id, query, pager.cursor),
    queryFn: () => getManagedBindingCandidates(role, model.id, query, pager.cursor),
    retry: false,
  });
  const capabilityLost = isUnauthorized(bindings.error) || isForbidden(bindings.error)
    || isUnauthorized(candidates.error) || isForbidden(candidates.error);
  useEffect(() => {
    if (capabilityLost) onCapabilityLoss?.();
  }, [capabilityLost, onCapabilityLoss]);
  const reconcile = async () => {
    await Promise.all([bindings.refetch(), candidates.refetch()]);
  };
  const add = useRetainedOperation(
    (
      input: {
        selections: { donation_key_id: string; upstream_model_id: string }[];
        revision: string;
      },
      key,
    ) => addManagedBindings(role, model.id, input.revision, input.selections, key),
    reconcile,
    charityKeys.root(role),
  );
  const order = useRetainedOperation(
    (input: { ids: string[]; revision: string }, key) =>
      orderManagedBindings(role, model.id, input.revision, input.ids, key),
    reconcile,
    charityKeys.root(role),
  );
  const remove = useRetainedOperation(
    (input: { id: string; revision: string }, key) =>
      deleteManagedBinding(role, model.id, input.id, input.revision, key),
    reconcile,
    charityKeys.root(role),
  );
  const mutationCapabilityLost = isUnauthorized(add.error) || isForbidden(add.error)
    || isUnauthorized(order.error) || isForbidden(order.error)
    || isUnauthorized(remove.error) || isForbidden(remove.error);
  useEffect(() => {
    if (mutationCapabilityLost) onCapabilityLoss?.();
  }, [mutationCapabilityLost, onCapabilityLoss]);
  const move = (index: number, offset: number) => {
    if (!bindings.data) return;
    const ids = bindings.data.bindings.map((entry) => entry.id);
    [ids[index], ids[index + offset]] = [ids[index + offset], ids[index]];
    order.mutate({ ids, revision: bindings.data.binding_revision });
  };
  const chosen = useMemo(
    () =>
      candidates.data?.data
        .filter((entry) => selected[`${entry.donation_key_id}:${entry.upstream_model_id}`])
        .map((entry) => ({
          donation_key_id: entry.donation_key_id,
          upstream_model_id: entry.upstream_model_id,
        })) ?? [],
    [candidates.data, selected],
  );
  if (capabilityLost || mutationCapabilityLost) {
    return <Card><p className="field-error" role="alert">Charity management access is no longer available.</p></Card>;
  }
  return (
    <Card>
      <h3>Ordered bindings</h3>
      {bindings.isPending ? (
        <LoadingState />
      ) : bindings.error ? (
        <ErrorState error={bindings.error} onRetry={() => void bindings.refetch()} />
      ) : bindings.data.bindings.length === 0 ? (
        <EmptyState title="No bindings" body="Add one or more role-safe candidates below." />
      ) : (
        <div className="ops-table-scroll">
          <table className="ops-table">
            <thead>
              <tr>
                <th>Order</th>
                <th>Source</th>
                <th>Upstream model</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {bindings.data.bindings.map((entry, index) => (
                <tr key={entry.id}>
                  <td>{entry.ord}</td>
                  <td>
                    {entry.source.connector_type} · {entry.source.canonical_base_url} ·{' '}
                    {entry.source.display_head}…{entry.source.display_tail}
                  </td>
                  <td>{entry.upstream_model_id}</td>
                  <td>
                    <button
                      className="btn btn-secondary"
                      type="button"
                      disabled={index === 0 || order.isPending}
                      onClick={() => move(index, -1)}
                    >
                      Up
                    </button>
                    <button
                      className="btn btn-secondary"
                      type="button"
                      disabled={index === bindings.data.bindings.length - 1 || order.isPending}
                      onClick={() => move(index, 1)}
                    >
                      Down
                    </button>
                    <button
                      className="btn btn-danger"
                      type="button"
                      disabled={remove.isPending}
                      onClick={() =>
                        remove.mutate({ id: entry.id, revision: bindings.data.binding_revision })
                      }
                    >
                      Remove
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <h3>Binding candidates</h3>
      <form
        className="ops-toolbar"
        onSubmit={(event) => {
          event.preventDefault();
          pager.reset();
          setQuery(queryDraft.trim());
        }}
      >
        <label>
          <span>Safe source/model search</span>
          <input value={queryDraft} onChange={(event) => setQueryDraft(event.target.value)} />
        </label>
        <button className="btn btn-secondary" type="submit">
          Search
        </button>
      </form>
      {candidates.isPending ? (
        <LoadingState />
      ) : candidates.error ? (
        <ErrorState error={candidates.error} onRetry={() => void candidates.refetch()} />
      ) : candidates.data.data.length === 0 ? (
        <EmptyState title="No candidates" body="No approved role-safe candidate matches." />
      ) : (
        <>
          {candidates.data.data.map((entry) => {
            const key = `${entry.donation_key_id}:${entry.upstream_model_id}`;
            return (
              <label key={key} className="ops-subcard">
                <input
                  type="checkbox"
                  checked={Boolean(selected[key])}
                  onChange={(event) => setSelected({ ...selected, [key]: event.target.checked })}
                />{' '}
                {entry.source.connector_type} · {entry.source.canonical_base_url} ·{' '}
                {entry.upstream_model_id} ({entry.source_types.join('/')})
              </label>
            );
          })}
          <CursorPagination
            page={pager.page}
            nextCursor={candidates.data.next_cursor}
            onPrevious={pager.previous}
            onNext={pager.next}
          />
        </>
      )}
      <div className="ops-actions">
        <button
          className="btn btn-primary"
          type="button"
          disabled={!bindings.data || chosen.length === 0 || add.isPending}
          onClick={() =>
            bindings.data &&
            add.mutate(
              { selections: chosen, revision: bindings.data.binding_revision },
              { onSuccess: () => setSelected({}) },
            )
          }
        >
          Add selected atomically
        </button>
      </div>
      {add.error ? (
        <ErrorState error={add.error} />
      ) : order.error ? (
        <ErrorState error={order.error} />
      ) : remove.error ? (
        <ErrorState error={remove.error} />
      ) : null}
    </Card>
  );
}

function ModelsPanel({ role, onCapabilityLoss }: { role: CharityRole; onCapabilityLoss?: () => void }) {
  const pager = useCursorPager();
  const [queryDraft, setQueryDraft] = useState('');
  const [query, setQuery] = useState('');
  const [enabled, setEnabled] = useState('');
  const [selected, setSelected] = useState<CharityModel | null>(null);
  const models = useQuery({
    queryKey: charityKeys.models(role, query, enabled, pager.cursor),
    queryFn: () => getManagedCharityModels(role, query, enabled, pager.cursor),
    retry: false,
  });
  const capabilityLost = isUnauthorized(models.error) || isForbidden(models.error);
  useEffect(() => {
    if (capabilityLost) onCapabilityLoss?.();
  }, [capabilityLost, onCapabilityLoss]);
  const refresh = async () => {
    const result = await models.refetch();
    if (selected && result.data)
      setSelected(result.data.data.find((item) => item.id === selected.id) ?? null);
  };
  if (capabilityLost) {
    return <Card><p className="field-error" role="alert">Charity management access is no longer available.</p></Card>;
  }
  return (
    <div className="ops-stack">
      <ModelForm role={role} refresh={refresh} onCapabilityLoss={onCapabilityLoss} />
      <Card>
        <form
          className="ops-toolbar"
          onSubmit={(event) => {
            event.preventDefault();
            pager.reset();
            setQuery(queryDraft.trim());
          }}
        >
          <label>
            <span>Search models</span>
            <input value={queryDraft} onChange={(event) => setQueryDraft(event.target.value)} />
          </label>
          <label>
            <span>Enabled</span>
            <select
              value={enabled}
              onChange={(event) => {
                pager.reset();
                setEnabled(event.target.value);
              }}
            >
              <option value="">All</option>
              <option value="true">Enabled</option>
              <option value="false">Disabled</option>
            </select>
          </label>
          <button className="btn btn-secondary" type="submit">
            Apply
          </button>
        </form>
        {models.isPending ? (
          <LoadingState />
        ) : models.error ? (
          <ErrorState error={models.error} onRetry={() => void models.refetch()} />
        ) : models.data.data.length === 0 ? (
          <EmptyState title="No charity models" body="No logical model matches this filter." />
        ) : (
          <>
            <div className="ops-table-scroll">
              <table className="ops-table">
                <thead>
                  <tr>
                    <th>Model</th>
                    <th>State</th>
                    <th>Pricing</th>
                    <th>Bindings</th>
                    <th>Open</th>
                  </tr>
                </thead>
                <tbody>
                  {models.data.data.map((model) => (
                    <tr key={model.id}>
                      <td>
                        {model.full_name}
                        <small>{model.id}</small>
                      </td>
                      <td>
                        <StatusBadge
                          active={model.enabled}
                          label={model.enabled ? 'enabled' : 'disabled'}
                        />
                      </td>
                      <td>{model.pricing.mode}</td>
                      <td>{model.binding_count}</td>
                      <td>
                        <button
                          className="btn btn-secondary"
                          type="button"
                          onClick={() => setSelected(model)}
                        >
                          Manage
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <CursorPagination
              page={pager.page}
              nextCursor={models.data.next_cursor}
              onPrevious={pager.previous}
              onNext={pager.next}
            />
          </>
        )}
      </Card>
      {selected && !capabilityLost ? (
        <>
          <ModelForm
            key={`model:${selected.id}:${selected.revision}`}
            role={role}
            model={selected}
            refresh={refresh}
            onDeleted={() => setSelected(null)}
            onCapabilityLoss={onCapabilityLoss}
          />
          <BindingsPanel
            key={`bindings:${selected.id}:${selected.binding_revision}`}
            role={role}
            model={selected}
            onCapabilityLoss={onCapabilityLoss}
          />
        </>
      ) : null}
    </div>
  );
}

export function CharityManagement({
  frame,
  onCapabilityLoss,
}: {
  frame: CharityRole;
  onCapabilityLoss?: () => void;
}) {
  const [section, setSection] = useState<'donations' | 'models'>('donations');
  const [capabilityLost, setCapabilityLost] = useState(false);
  const clearCapability = useCallback(() => {
    setCapabilityLost(true);
    onCapabilityLoss?.();
  }, [onCapabilityLoss]);
  if (capabilityLost) {
    return (
      <Card>
        <p className="field-error" role="alert">
          Charity management access is no longer available.
        </p>
      </Card>
    );
  }
  return (
    <div className="ops-stack">
      <div className="ops-tabs" role="tablist" aria-label="Charity management sections">
        <button
          className={section === 'donations' ? 'btn btn-primary' : 'btn btn-secondary'}
          type="button"
          role="tab"
          aria-selected={section === 'donations'}
          onClick={() => setSection('donations')}
        >
          Donations
        </button>
        <button
          className={section === 'models' ? 'btn btn-primary' : 'btn btn-secondary'}
          type="button"
          role="tab"
          aria-selected={section === 'models'}
          onClick={() => setSection('models')}
        >
          Models and bindings
        </button>
      </div>
      {section === 'donations' ? (
        <DonationsPanel role={frame} onCapabilityLoss={clearCapability} />
      ) : (
        <ModelsPanel role={frame} onCapabilityLoss={clearCapability} />
      )}
    </div>
  );
}
