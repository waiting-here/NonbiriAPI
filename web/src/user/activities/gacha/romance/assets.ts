import type { RomanceTheme } from './layout';

export function romanceAssetSources(theme: RomanceTheme, highResolution = false, dark = false) {
  const claude = theme === 'elopement';
  const id = claude ? 'p-claude' : 'q-glm';
  const names = claude
    ? ['background', 'offer', 'arrival', 'closed', 'clean']
    : ['background', 'character', 'hair-backing-v3'];
  return Object.fromEntries(
    names.map((name) => {
      // The character plates retain their original pixels in both display qualities.
      const high = highResolution && name === 'background' ? 'hires/' : '';
      const filename = name === 'background' ? `background-${dark ? 'dark' : 'light'}` : name;
      return [name, `/assets/gacha/${high}motion/${id}/${filename}.webp`];
    }),
  );
}
