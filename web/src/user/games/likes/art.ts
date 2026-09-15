import portraits from '@shared/assets/game-heroes/rps.webp';
import fishing from '@shared/assets/game-heroes/fishing.webp';
import chibi from '@shared/assets/game-fishing/blue-fat-fish.png';
import mark from '@shared/assets/nonbiri-mark.svg';
import type { RoleID, ModeCatalog } from './catalog';
import { ROLES } from './catalog';
import { artReplacements } from './artReplacements';

export type Pose =
  'portrait' | 'chibi_idle' | 'chibi_stunned' | 'chibi_overloaded' | 'win' | 'loss' | 'draw';
export interface ArtSlot {
  readonly key: string;
  readonly source: string;
  readonly ratio: '2:3' | '1:1' | '3:2';
  readonly focus: readonly [number, number];
  readonly placeholder: boolean;
  readonly transparentRequired: boolean;
  readonly sourceFile: string;
}
const roleImages: Record<
  RoleID,
  { source: string; focus: readonly [number, number]; sourceFile: string }
> = {
  ChatGPT: {
    source: portraits,
    focus: [0.18, 0.5],
    sourceFile: 'web/src/shared/assets/game-heroes/rps.webp',
  },
  Claude: {
    source: portraits,
    focus: [0.53, 0.5],
    sourceFile: 'web/src/shared/assets/game-heroes/rps.webp',
  },
  Gemini: {
    source: portraits,
    focus: [0.89, 0.5],
    sourceFile: 'web/src/shared/assets/game-heroes/rps.webp',
  },
  GLM: {
    source: portraits,
    focus: [0.5, 0.5],
    sourceFile: 'web/src/shared/assets/game-heroes/rps.webp',
  },
  DeepSeek: {
    source: fishing,
    focus: [0.22, 0.5],
    sourceFile: 'web/src/shared/assets/game-heroes/fishing.webp',
  },
};
const roleSkills: Record<RoleID, readonly string[]> = {
  ChatGPT: ['GPT01', 'GPT21', 'GPT22', 'GPT41', 'GPT42', 'GPT43', 'GPT44', 'GPT61'],
  Claude: ['CLA01', 'CLA21', 'CLA22', 'CLA23', 'CLA41', 'CLA42', 'CLA61', 'CLA62'],
  Gemini: ['GEM01', 'GEM02', 'GEM21', 'GEM22', 'GEM41', 'GEM42', 'GEM61', 'GEM62'],
  GLM: ['GLM01', 'GLM21', 'GLM22', 'GLM23', 'GLM41', 'GLM42', 'GLM61', 'GLM62'],
  DeepSeek: ['DS01', 'DS21', 'DS22', 'DS23', 'DS41', 'DS42', 'DS43', 'DS61'],
};
const publicSkills = ['PUB01', 'PUB02', 'PUB21', 'PUB22', 'PUB41', 'PUB42', 'PUB61', 'PUB62'];
const placeholders: Readonly<Record<string, ArtSlot>> = Object.fromEntries([
  ...ROLES.flatMap((role) =>
    (
      [
        'portrait',
        'chibi_idle',
        'chibi_stunned',
        'chibi_overloaded',
        'win',
        'loss',
        'draw',
      ] as const
    ).map((pose) => {
      const key = `role.${role}.${pose}`,
        small = pose.startsWith('chibi_'),
        image = roleImages[role];
      const slot: ArtSlot = {
        key,
        source: small ? chibi : image.source,
        ratio: small ? '1:1' : '2:3',
        focus: small ? [0.5, 0.5] : image.focus,
        placeholder: true,
        transparentRequired: true,
        sourceFile: small
          ? 'web/src/shared/assets/game-fishing/blue-fat-fish.png'
          : image.sourceFile,
      };
      return [key, slot];
    }),
  ),
  ...ROLES.flatMap((role) =>
    [...roleSkills[role], ...publicSkills, ...(role === 'Gemini' ? [] : ['GEM01'])].map((skill) => {
      const key = `cast.${role}.${skill}`,
        image = roleImages[role];
      const slot: ArtSlot = {
        key,
        source: image.source,
        ratio: '3:2',
        focus: image.focus,
        placeholder: true,
        transparentRequired: true,
        sourceFile: image.sourceFile,
      };
      return [key, slot];
    }),
  ),
  ...Array.from({ length: 8 }, (_, i) => {
    const key = `harness.H0${i + 1}`;
    const slot: ArtSlot = {
      key,
      source: mark,
      ratio: '1:1',
      focus: [0.5, 0.5],
      placeholder: true,
      transparentRequired: true,
      sourceFile: 'web/src/shared/assets/nonbiri-mark.svg',
    };
    return [key, slot];
  }),
]);
export const artRegistry: Readonly<Record<string, ArtSlot>> = Object.fromEntries(
  Object.entries(placeholders).map(([key, slot]) => [key, { ...slot, ...artReplacements[key] }]),
);
export function assertArtCoverage(catalog: ModeCatalog): void {
  if (Object.keys(artRegistry).length !== 127) throw new Error('Incomplete art registry');
  for (const role of catalog.roles)
    for (const skill of catalog.skills.filter(
      (s) => s.owner === role.id || s.owner === '全局公共' || s.id === 'GEM01',
    ))
      if (!artRegistry[`cast.${role.id}.${skill.id}`]) throw new Error('Missing cast art');
}
export function characterSlot(role: RoleID, pose: Pose): ArtSlot {
  return artRegistry[`role.${role}.${pose}`];
}
export function castSlot(role: RoleID, skill: string): ArtSlot | null {
  return artRegistry[`cast.${role}.${skill}`] ?? null;
}
