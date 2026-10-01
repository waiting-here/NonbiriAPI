import { boolean, nullableDecimalID, nullableString, type WireRecord } from './wire';

export interface LogOrigin {
  origin_user_id?: string | null;
  origin_discord_id?: string | null;
  origin_deleted?: boolean;
  origin_unknown?: boolean;
  history_record_id?: string | null;
}
export const logOriginFields = [
  'origin_user_id',
  'origin_discord_id',
  'origin_deleted',
  'origin_unknown',
  'history_record_id',
] as const;
export function normalizeLogOrigin(root: WireRecord): LogOrigin {
  return {
    origin_user_id: nullableDecimalID(root.origin_user_id ?? null, 'original user id'),
    origin_discord_id: nullableString(root.origin_discord_id ?? null, 'original Discord id', {
      min: 1,
      max: 128,
      bytes: 128,
    }),
    origin_deleted:
      root.origin_deleted === undefined
        ? root.user_id === null
        : boolean(root.origin_deleted, 'original account deleted'),
    origin_unknown:
      root.origin_unknown === undefined
        ? true
        : boolean(root.origin_unknown, 'original identity unknown'),
    history_record_id: nullableDecimalID(
      root.history_record_id ?? null,
      'historical account record',
    ),
  };
}
