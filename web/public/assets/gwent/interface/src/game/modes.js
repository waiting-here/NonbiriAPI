export const matchModes = Object.freeze({
  local: { name: 'AI 观战', opponent: 'LOCAL AI', needsServer: false },
});
export function matchMode(id) {
  if (!Object.hasOwn(matchModes, id)) throw Error('未知对战模式');
  return matchModes[id];
}
