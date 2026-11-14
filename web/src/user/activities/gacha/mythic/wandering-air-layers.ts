export type WanderingAirLayer = {
  name: string;
  kind: 'bird' | 'cloud';
  rect: readonly [number, number, number, number];
  path: string;
  speed?: number;
  flap?: number;
  period?: number;
  extent?: number;
};
export const wanderingAirLayers: readonly WanderingAirLayer[] = [
  {
    name: 'crane-upper',
    speed: 6.1,
    flap: 2.1,
    kind: 'bird',
    rect: [169, 240, 106, 80],
    path: 'm-air-crane-upper.webp',
  },
  {
    name: 'gull-upper-left',
    speed: 5.3,
    flap: 0.7,
    kind: 'bird',
    rect: [125, 232, 36, 27],
    path: 'm-air-gull-upper-left.webp',
  },
  {
    name: 'gull-upper-right',
    speed: 5.6,
    flap: 0.7,
    kind: 'bird',
    rect: [198, 229, 42, 22],
    path: 'm-air-gull-upper-right.webp',
  },
  {
    name: 'crane-right',
    speed: 8.4,
    flap: 2.3,
    kind: 'bird',
    rect: [919, 851, 101, 76],
    path: 'm-air-crane-right.webp',
  },
  {
    name: 'gull-right',
    speed: 7.8,
    flap: 1.2,
    kind: 'bird',
    rect: [881, 902, 71, 39],
    path: 'm-air-gull-right.webp',
  },
  {
    name: 'crane-lower',
    speed: 10.2,
    flap: 2.4,
    kind: 'bird',
    rect: [604, 1019, 105, 71],
    path: 'm-air-crane-lower.webp',
  },
  {
    name: 'gull-low-pair',
    speed: 6.8,
    flap: 0.8,
    kind: 'bird',
    rect: [635, 1198, 74, 46],
    path: 'm-air-gull-low-pair.webp',
  },
  {
    name: 'gull-low-three',
    speed: 7.1,
    flap: 0.7,
    kind: 'bird',
    rect: [795, 1271, 44, 28],
    path: 'm-air-gull-low-three.webp',
  },
  {
    name: 'gull-low-four',
    speed: 7.5,
    flap: 0.7,
    kind: 'bird',
    rect: [764, 1296, 39, 27],
    path: 'm-air-gull-low-four.webp',
  },
  {
    name: 'gull-low-five',
    speed: 7.9,
    flap: 0.7,
    kind: 'bird',
    rect: [739, 1313, 37, 27],
    path: 'm-air-gull-low-five.webp',
  },
  {
    name: 'crane-near',
    speed: -12,
    flap: 2.1,
    kind: 'bird',
    rect: [797, 1331, 102, 66],
    path: 'm-air-crane-near.webp',
  },
  {
    name: 'crane-blurred',
    speed: -15,
    flap: 1.8,
    kind: 'bird',
    rect: [905, 1227, 119, 145],
    path: 'm-air-crane-blurred.webp',
  },
  {
    name: 'cloud-front-whale',
    period: 83,
    extent: 14,
    kind: 'cloud',
    rect: [321, 308, 245, 176],
    path: 'm-air-cloud-front-whale.webp',
  },
  {
    name: 'cloud-middle-left',
    period: 107,
    extent: 12,
    kind: 'cloud',
    rect: [0, 570, 259, 188],
    path: 'm-air-cloud-middle-left.webp',
  },
  {
    name: 'cloud-near-left',
    period: 71,
    extent: 16,
    kind: 'cloud',
    rect: [0, 933, 210, 209],
    path: 'm-air-cloud-near-left.webp',
  },
];
