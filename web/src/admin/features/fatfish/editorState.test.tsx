import { fireEvent, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { blankLevel, rect, unit } from './draft';
import { readiness, geometryIssues, issueSelection, nearestPlacement, describeIssue } from './editorState';
import { NumberField } from './NumberField';
import { useFatFishText } from './copy';

it('uses missing legacy fish, text and pending geometry for the same readiness decision', () => {
  const level = blankLevel();
  expect(readiness(level, 'Level', '', 5, false)).toMatchObject({ ready: false, missingFish: 4 });
  expect(readiness(level, '', '', 1, false)).toMatchObject({ ready: false, missingTitle: true });
  expect(readiness(level, 'Level', '', 1, true)).toMatchObject({ ready: false, pending: true });
  expect(readiness(level, 'Level', '', 1, false).ready).toBe(true);
});

it('locates malformed polygons even when the engine message only names an edge', () => {
  const level = blankLevel();
  level.solids.push({ id: 9, polygon: { outer: [{ x: 4000, y: 4000 }, { x: 6000, y: 6000 }, { x: 6000, y: 4000 }, { x: 4000, y: 6000 }], holes: [] } });
  const issue = geometryIssues(level).find((issue) => issue.selection?.id === 9);
  expect(issue).toBeDefined();
  expect(issueSelection('hole[0] edge[2] intersects edge[3]', level, { kind: 'solids', id: 9 })).toEqual({ kind: 'solids', id: 9 });
});

it('moves an added hazard away from a bowl when the initial location is invalid', () => {
  const level = blankLevel(), bowl = level.bowls[0];
  level.hazards.push({ id: 9, polygon: rect(unit(380), unit(180), unit(40), unit(40)) });
  const next = nearestPlacement(level, { kind: 'hazards', id: 9 }, { x: unit(380), y: unit(180) });
  expect(next.hazards[0].polygon).not.toEqual(level.hazards[0].polygon);
  expect(next.bowls[0]).toEqual(bowl);
  expect(readiness(next, 'Level', '', 0, false).ready).toBe(true);
});

it('commits numeric edits once on blur or Enter instead of once per digit', async () => {
  const change = vi.fn();
  await renderWithProviders(<NumberField label="Duration" value={90} onChange={change} />, { station: 'admin', role: 'admin' });
  const input = screen.getByLabelText('Duration');
  fireEvent.focus(input);
  for (const value of ['1', '12', '120']) fireEvent.change(input, { target: { value } });
  expect(change).not.toHaveBeenCalled();
  fireEvent.blur(input);
  expect(change.mock.calls).toEqual([[120]]);
});

function IssueCopy() {
  const text = useFatFishText();
  return <p>{describeIssue('fish[0] center begins inside an object', text)}</p>;
}
it('describes engine failures in the selected language', async () => {
  await renderWithProviders(<IssueCopy />, { station: 'admin', role: 'admin', locale: 'zh' });
  expect(screen.getByText('小鱼起点在障碍或碗中，请移动起点。')).toBeInTheDocument();
});
