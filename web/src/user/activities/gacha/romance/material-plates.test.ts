import { describe, expect, it } from 'vitest';
import {
  materialFrame,
  materialImpulse,
  materialPointWeight,
  materialPose,
} from './material-plates';
import type { MaterialPlate } from './material-plates';

const cloth: MaterialPlate = {
  name: 'cloth',
  file: 'cloth.webp',
  crop: [0, 0, 100, 100],
  polygon: [
    [0, 0],
    [100, 0],
    [100, 100],
    [0, 100],
  ],
  root: 0,
  end: 100,
  vector: [20, -10],
  delay: 0,
  seam: 12,
  material: 'cloth',
};

describe('independent material attachments', () => {
  it('releases the free silhouette while keeping an authored open seam pinned', () => {
    const panel = {
      ...cloth,
      seams: [
        [
          [0, 0],
          [100, 0],
        ],
      ] as const,
    };
    expect(materialPointWeight(panel, [0, 100])).toBe(1);
    expect(materialPointWeight(panel, [100, 100])).toBe(1);
    expect(materialPointWeight(panel, [50, 0])).toBe(0);
    // With legacy polygon seams, the perimeter stays pinned.
    expect(materialPointWeight(cloth, [0, 100])).toBe(0);
  });

  it('settles lifted fabric toward its gravity pose after the gust', () => {
    const panel = { ...cloth, rest: [0, 8] as const };
    expect(materialPose(panel, 25, 26).offset).toEqual([0, 8]);
    expect(materialPose(panel, 7, 26).offset).toEqual([20, -10]);
    expect(materialPose(panel, 19, 26).offset[1]).toBeGreaterThan(8);
    expect(materialPose(panel, 26, 26)).toEqual(materialPose(panel, 0, 26));
  });

  it('keeps old motion and descriptive parent metadata compatible', () => {
    const legacy = { ...cloth, parent: 'fixed adopted waist seam' };
    const pose = materialFrame([legacy], 7, 26).get('cloth')!;
    expect(pose.inherited).toEqual([0, 0]);
    expect(pose.offset).toEqual(cloth.vector.map((v) => v * materialImpulse(7, 26, 0, true)));
  });

  it('carries a rigid ornament with the actual moving attachment through nested parents', () => {
    const panel = { ...cloth, rootOnly: true };
    const chain: MaterialPlate = {
      ...cloth,
      name: 'chain',
      rigid: true,
      vector: [0, 0],
      parent: { name: 'cloth', at: [50, 50] },
    };
    const jewel: MaterialPlate = {
      ...chain,
      name: 'jewel',
      parent: { name: 'chain', at: [50, 90] },
    };
    const poses = materialFrame([jewel, panel, chain], 7, 26);
    expect(poses.get('chain')!.inherited).toEqual([10, -5]);
    expect(poses.get('jewel')!.inherited).toEqual([10, -5]);
    expect(poses.get('jewel')!.offset).toEqual([0, 0]);
  });

  it('rejects broken or cyclic attachments instead of silently detaching ornaments', () => {
    expect(() =>
      materialFrame([{ ...cloth, parent: { name: 'missing', at: [0, 0] } }], 0, 26),
    ).toThrow('material-parent-unavailable');
    expect(() =>
      materialFrame([{ ...cloth, parent: { name: 'cloth', at: [0, 0] } }], 0, 26),
    ).toThrow('material-parent-cycle');
  });
});
