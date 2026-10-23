import { regionWeight } from './motion';

type Point = readonly [number, number];
type Node = {
  x: number;
  y: number;
  z: number;
  px: number;
  py: number;
  pz: number;
  ox: number;
  oy: number;
  mass: number;
};
type Edge = { a: number; b: number; length: number; stiffness: number };
type Triangle = readonly [number, number, number];
type Sample = { offset: number; nodes: Triangle; weights: [number, number, number]; gain: number };

const smooth = (v: number) => {
  const t = Math.max(0, Math.min(1, v));
  return t * t * (3 - 2 * t);
};
export function walkingWind(seconds: number) {
  const t = ((seconds % 24) + 24) % 24;
  return (
    0.1 * Math.sin((t * Math.PI) / 4) +
    smooth((t - 4) / 2.5) * (1 - smooth((t - 9) / 4)) -
    0.42 * smooth((t - 16) / 2) * (1 - smooth((t - 20) / 4))
  );
}

// Each strip follows an authored hair bundle or cloth panel. Across-strip and
// diagonal constraints preserve its width; the root row is fixed to its seam.
const strips: { rows: readonly (readonly Point[])[]; strength: number }[] = [
  {
    rows: [
      [
        [302, 292],
        [346, 312],
      ],
      [
        [205, 387],
        [295, 433],
      ],
      [
        [112, 481],
        [274, 530],
      ],
      [
        [49, 570],
        [240, 645],
      ],
      [
        [62, 705],
        [185, 718],
      ],
    ],
    strength: 32,
  },
  {
    rows: [
      [
        [568, 229],
        [639, 226],
      ],
      [
        [604, 349],
        [796, 342],
      ],
      [
        [705, 488],
        [965, 439],
      ],
      [
        [754, 589],
        [1025, 582],
      ],
      [
        [755, 704],
        [1020, 779],
      ],
    ],
    strength: 39,
  },
  {
    rows: [
      [
        [334, 579],
        [390, 607],
      ],
      [
        [279, 680],
        [355, 713],
      ],
      [
        [231, 775],
        [333, 825],
      ],
      [
        [255, 850],
        [306, 874],
      ],
    ],
    strength: 24,
  },
  {
    rows: [
      [
        [553, 718],
        [710, 742],
      ],
      [
        [594, 778],
        [814, 790],
      ],
      [
        [649, 858],
        [895, 873],
      ],
      [
        [744, 976],
        [926, 1042],
      ],
    ],
    strength: 29,
  },
];
const protectedRegions: readonly (readonly Point[])[] = [
  [
    [292, 305],
    [383, 302],
    [399, 350],
    [365, 433],
    [334, 491],
    [301, 444],
  ],
  [
    [422, 574],
    [487, 566],
    [508, 620],
    [477, 656],
    [435, 641],
  ],
  [
    [530, 335],
    [593, 359],
    [645, 402],
    [675, 490],
    [721, 561],
    [748, 684],
    [729, 735],
    [643, 736],
    [592, 692],
    [565, 597],
    [544, 510],
  ],
  [
    [878, 815],
    [920, 827],
    [930, 861],
    [887, 864],
  ],
];

function barycentric(p: Point, a: Node, b: Node, c: Node) {
  const den = (b.oy - c.oy) * (a.ox - c.ox) + (c.ox - b.ox) * (a.oy - c.oy);
  const u = ((b.oy - c.oy) * (p[0] - c.ox) + (c.ox - b.ox) * (p[1] - c.oy)) / den;
  const v = ((c.oy - a.oy) * (p[0] - c.ox) + (a.ox - c.ox) * (p[1] - c.oy)) / den;
  return [u, v, 1 - u - v] as [number, number, number];
}

export function createWalkingRig() {
  const nodes: Node[] = [],
    edges: Edge[] = [],
    triangles: Triangle[] = [],
    boundaries: Point[][] = [],
    forces: number[] = [];
  for (const strip of strips) {
    const start = nodes.length;
    const boundary = [
      ...strip.rows.map((row) => row[0]),
      ...strip.rows.map((row) => row[1]).reverse(),
    ];
    strip.rows.forEach((row, y) =>
      row.forEach(([x, py]) => {
        nodes.push({ x, y: py, z: 0, px: x, py, pz: 0, ox: x, oy: py, mass: y === 0 ? 0 : 1 });
        forces.push((strip.strength * y) / (strip.rows.length - 1));
      }),
    );
    const edge = (a: number, b: number, stiffness = 0.9) =>
      edges.push({
        a,
        b,
        length: Math.hypot(nodes[a].ox - nodes[b].ox, nodes[a].oy - nodes[b].oy),
        stiffness,
      });
    for (let y = 0; y < strip.rows.length; y++) {
      const a = start + y * 2,
        b = a + 1;
      edge(a, b);
      if (y > 0) {
        edge(a - 2, a);
        edge(b - 2, b);
        edge(a - 2, b);
        edge(b - 2, a);
        triangles.push([a - 2, b - 2, a], [b - 2, b, a]);
        boundaries.push(boundary, boundary);
      }
      if (y > 1) {
        edge(a - 4, a, 0.12);
        edge(b - 4, b, 0.12);
      }
    }
  }
  const width = 256,
    height = 384,
    pixels = new Uint8Array(width * height * 4),
    samples: Sample[] = [];
  const torso: Point[] = [
    [353, 305],
    [429, 305],
    [500, 397],
    [513, 525],
    [421, 587],
    [350, 555],
    [324, 431],
  ];
  const vertex = ([ox, oy]: Point) => ({ ox, oy }) as Node;
  for (let y = 0; y < height; y++)
    for (let x = 0; x < width; x++) {
      const offset = (y * width + x) * 4;
      pixels[offset] = pixels[offset + 1] = 128;
      const p: Point = [(x + 0.5) * 4, (y + 0.5) * 4];
      // Keep hands, pocket contact and the complete backpack independent of wind.
      const pin = Math.max(...protectedRegions.map((poly) => regionWeight(p[0], p[1], poly, 6)));
      if (pin >= 0.999) continue;
      for (let i = 0; i < torso.length; i++) {
        const weights = barycentric(
          p,
          vertex([406, 446]),
          vertex(torso[i]),
          vertex(torso[(i + 1) % torso.length]),
        );
        if (weights.every((w) => w >= 0 && w <= 1)) {
          pixels[offset + 2] = Math.round(weights[0] * (1 - pin) * 255);
          break;
        }
      }
      for (let i = 0; i < triangles.length; i++) {
        const tri = triangles[i];
        const weights = barycentric(p, nodes[tri[0]], nodes[tri[1]], nodes[tri[2]]);
        if (weights.every((w) => w >= 0 && w <= 1)) {
          // Blend into the stationary painting where a panel ends inside a fold.
          samples.push({
            offset,
            nodes: tri,
            weights,
            gain: (1 - pin) * regionWeight(p[0], p[1], boundaries[i], 24),
          });
          break;
        }
      }
    }
  let simulated = 0,
    angle = 0,
    velocity = 0;
  function step(t: number) {
    const dt = 1 / 120,
      wind = walkingWind(t);
    nodes.forEach((n, i) => {
      if (!n.mass) return;
      const vx = (n.x - n.px) * 0.97,
        vy = (n.y - n.py) * 0.97,
        vz = (n.z - n.pz) * 0.975;
      n.px = n.x;
      n.py = n.y;
      n.pz = n.z;
      n.x += vx - (n.x - n.ox) * 3 * dt * dt;
      n.y += vy - (n.y - n.oy) * 3 * dt * dt;
      n.z += vz + (wind * forces[i] * 2.7 - n.z * 2.2) * dt * dt;
    });
    for (let pass = 0; pass < 7; pass++)
      for (const e of edges) {
        const a = nodes[e.a],
          b = nodes[e.b],
          mass = a.mass + b.mass;
        if (!mass) continue;
        const dx = b.x - a.x,
          dy = b.y - a.y,
          dz = b.z - a.z,
          d = Math.hypot(dx, dy, dz);
        const error = ((d - e.length) / Math.max(0.0001, d) / mass) * e.stiffness;
        a.x += dx * error * a.mass;
        a.y += dy * error * a.mass;
        a.z += dz * error * a.mass;
        b.x -= dx * error * b.mass;
        b.y -= dy * error * b.mass;
        b.z -= dz * error * b.mass;
      }
    // A damped rigid pendulum, attached to the retained backpack ring.
    velocity += (wind * 0.63 - angle * 19 - velocity * 5.2) * dt;
    angle = Math.max(-0.045, Math.min(0.045, angle + velocity * dt));
  }
  function reset() {
    for (const n of nodes) {
      n.x = n.px = n.ox;
      n.y = n.py = n.oy;
      n.z = n.pz = 0;
    }
    simulated = angle = velocity = 0;
  }
  function update(seconds: number) {
    const target = Math.max(0, seconds);
    if (target < simulated - 1 / 120) reset();
    while (simulated + 1 / 120 <= target) {
      simulated += 1 / 120;
      step(simulated);
    }
    for (const s of samples) {
      let dx = 0,
        dy = 0;
      for (let i = 0; i < 3; i++) {
        const n = nodes[s.nodes[i]];
        dx += (n.x - n.ox + n.z * 0.3) * s.weights[i];
        dy += (n.y - n.oy - n.z * 0.075) * s.weights[i];
      }
      pixels[s.offset] = Math.round(128 + Math.max(-15.5, Math.min(15.5, dx * s.gain)) * 8);
      pixels[s.offset + 1] = Math.round(128 + Math.max(-15.5, Math.min(15.5, dy * s.gain)) * 8);
    }
    return { width, height, pixels, angle };
  }
  function metrics() {
    return {
      maxEdgeError: Math.max(
        ...edges
          .filter((e) => e.stiffness > 0.5)
          .map((e) =>
            Math.abs(
              Math.hypot(
                nodes[e.a].x - nodes[e.b].x,
                nodes[e.a].y - nodes[e.b].y,
                nodes[e.a].z - nodes[e.b].z,
              ) - e.length,
            ),
          ),
      ),
      maxDisplacement: Math.max(
        ...nodes.map((n) => Math.hypot(n.x - n.ox + n.z * 0.3, n.y - n.oy - n.z * 0.075)),
      ),
      angle,
    };
  }
  return { update, metrics };
}
