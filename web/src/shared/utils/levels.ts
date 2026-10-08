export function formatLevelRanges(levels: readonly number[], separator = ', '): string {
  const ranges: string[] = [];
  for (let index = 0; index < levels.length; index += 1) {
    const start = levels[index];
    let end = start;
    while (levels[index + 1] === end + 1) end = levels[++index];
    ranges.push(start === end ? 'L' + start : 'L' + start + '–L' + end);
  }
  return ranges.join(separator);
}
