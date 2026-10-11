export const highResolutionArt = (src: string) =>
  src.replace('/assets/gacha/', '/assets/gacha/hires/');

export const artSourceSet = (src: string, standardWidth = 1672, highWidth = 3840) =>
  src + ' ' + standardWidth + 'w, ' + highResolutionArt(src) + ' ' + highWidth + 'w';
