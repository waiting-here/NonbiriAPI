export const heartThiefStance = {
  angle: (-2.4 * Math.PI) / 180,
  anchor: [421, 1437] as const,
  contentHeight: 1448,
  plateHeight: 1488,
};

export function heartThiefGroundPoint(x: number, y: number) {
  const [ax, ay] = heartThiefStance.anchor;
  const cosine = Math.cos(heartThiefStance.angle);
  const sine = Math.sin(heartThiefStance.angle);
  return [ax + (x - ax) * cosine - (y - ay) * sine, ay + (x - ax) * sine + (y - ay) * cosine];
}
