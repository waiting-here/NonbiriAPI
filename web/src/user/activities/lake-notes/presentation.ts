import {
  catalog,
  equipmentEffects,
  barHeight,
  biteWindow,
  type Profile,
  type Loadout,
  type FishType,
} from './rules';
import { catalogText, type LakeText } from './copy';

export const art = (kind: string, item = false) =>
  '/assets/lake-notes/' + (item ? 'item-' : 'fish-') + kind + '.webp';
export const label = (text: LakeText, group: string, id?: string) =>
  id ? catalogText(text, group, id) : text('none');
export function loadoutLabel(text: LakeText, l: Loadout) {
  return [l.rod, l.tackle1, l.tackle2, l.tackle3]
    .filter(Boolean)
    .map((id) => label(text, 'gear', id))
    .join(' / ');
}
export function loadoutStats(text: LakeText, p: Profile, loadout: Loadout) {
  const e = equipmentEffects(loadout),
    rod = catalog.GEAR[loadout.rod as keyof typeof catalog.GEAR];
  const selected = 'baitAllowed' in rod && rod.baitAllowed ? p.selectedBait : undefined;
  const wait = biteWindow(p, loadout, selected);
  return text('previewStats', {
    height: Math.floor(barHeight(p, e, loadout.rod, selected) * 568 + 0.5),
    loss: (15 * e.progressLoss).toFixed(1),
    min: wait.min.toFixed(2),
    max: wait.max.toFixed(2),
  });
}
export function fishConditions(text: LakeText, f: FishType) {
  return text('conditions', {
    location: label(text, 'locations', f.location),
    periods:
      f.periods?.map((id) => catalogText(text, 'periods', id, '')).join(' / ') ||
      text('allPeriods'),
    weather:
      f.weathers?.map((id) => catalogText(text, 'weathers', id, '')).join(' / ') ||
      text('allWeather'),
  });
}
