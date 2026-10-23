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
  force: number;
  response: number;
  damping: number;
  delay: number;
  projection: Point;
};
type Edge = { a: number; b: number; length: number; stiffness: number };
type Triangle = readonly [number, number, number];
type Sample = {
  offset: number;
  nodes: Triangle;
  weights: [number, number, number];
  gain: number;
  projection: Point;
};

const smooth = (v: number) => {
  const t = Math.max(0, Math.min(1, v));
  return t * t * (3 - 2 * t);
};
// Variation around the steady breeze already depicted in the painting.
export function walkingWind(seconds: number) {
  const t = ((seconds % 24) + 24) % 24;
  return (
    0.12 * Math.sin((t * Math.PI) / 4) +
    smooth((t - 1.5) / 3) * (1 - smooth((t - 7.5) / 4.5)) -
    0.5 * smooth((t - 14) / 3) * (1 - smooth((t - 19.5) / 4.5))
  );
}

// Each strip follows an authored hair bundle or cloth panel. Across-strip and
// diagonal constraints preserve its width; the root row is fixed to its seam.
const strips: { rows: readonly (readonly Point[])[] }[] = [
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
  },
];
const responses: readonly {
  force: number;
  response: number;
  damping: number;
  delay: number;
  projection: Point;
}[] = [
  { force: 46, response: 16, damping: 4.4, delay: 0.28, projection: [0.48, -0.5] },
  { force: 58, response: 16, damping: 4.4, delay: 0.36, projection: [0.42, -0.56] },
  { force: 38, response: 4.4, damping: 3.2, delay: 0.7, projection: [0.48, -0.46] },
  { force: 48, response: 4.4, damping: 3.2, delay: 0.8, projection: [0.48, -0.44] },
];
const protectedRegions: readonly (readonly Point[])[] = [
  [
    [340, 805],
    [470, 817],
    [551, 853],
    [634, 897],
    [688, 1060],
    [785, 1535],
    [315, 1535],
    [310, 1250],
    [420, 1170],
    [401, 1030],
    [354, 928],
  ],
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

function closestWeights(p: Point, a: Node, b: Node, c: Node) {
  const inside = barycentric(p, a, b, c);
  if (inside.every((w) => w >= 0 && w <= 1)) return { weights: inside, distance: 0 };
  const vertices = [a, b, c];
  let distance = Infinity;
  let weights: [number, number, number] = [0, 0, 0];
  for (let i = 0; i < 3; i++) {
    const j = (i + 1) % 3,
      from = vertices[i],
      to = vertices[j];
    const dx = to.ox - from.ox,
      dy = to.oy - from.oy;
    const t = Math.max(
      0,
      Math.min(1, ((p[0] - from.ox) * dx + (p[1] - from.oy) * dy) / (dx * dx + dy * dy)),
    );
    const d = Math.hypot(p[0] - from.ox - dx * t, p[1] - from.oy - dy * t);
    if (d < distance) {
      distance = d;
      weights = [0, 0, 0];
      weights[i] = 1 - t;
      weights[j] = t;
    }
  }
  return { weights, distance };
}

function outsideDistance(p: Point, polygon: readonly Point[]) {
  let inside = false,
    distance = Infinity;
  let a = polygon[polygon.length - 1];
  for (const b of polygon) {
    if (
      a[1] > p[1] !== b[1] > p[1] &&
      p[0] < ((a[0] - b[0]) * (p[1] - b[1])) / (a[1] - b[1]) + b[0]
    )
      inside = !inside;
    const dx = b[0] - a[0],
      dy = b[1] - a[1];
    const t = Math.max(
      0,
      Math.min(1, ((p[0] - a[0]) * dx + (p[1] - a[1]) * dy) / (dx * dx + dy * dy)),
    );
    distance = Math.min(distance, Math.hypot(p[0] - a[0] - dx * t, p[1] - a[1] - dy * t));
    a = b;
  }
  return inside ? 0 : distance;
}

export function createWalkingRig() {
  const nodes: Node[] = [],
    edges: Edge[] = [],
    triangles: Triangle[] = [],
    groups: { first: number; last: number; projection: Point }[] = [];
  for (const [index, strip] of strips.entries()) {
    const start = nodes.length,
      first = triangles.length,
      profile = responses[index];
    strip.rows.forEach((row, y) =>
      row.forEach(([x, py]) => {
        const progress = y / (strip.rows.length - 1);
        nodes.push({
          x,
          y: py,
          z: 0,
          px: x,
          py,
          pz: 0,
          ox: x,
          oy: py,
          mass: y === 0 ? 0 : 1,
          force: profile.force * progress,
          response: profile.response,
          damping: profile.damping,
          delay: profile.delay * progress,
          projection: profile.projection,
        });
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
      }
      if (y > 1) {
        edge(a - 4, a, 0.12);
        edge(b - 4, b, 0.12);
      }
    }
    groups.push({ first, last: triangles.length, projection: profile.projection });
  }
  const width = 256,
    height = 384,
    pixels = new Uint8Array(width * height * 4),
    samples: Sample[] = [],
    active: number[] = [],
    offsetsX = new Float32Array(width * height),
    offsetsY = new Float32Array(width * height);
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
      const windPin = Math.max(
        ...protectedRegions.map((poly) => 1 - smooth(outsideDistance(p, poly) / 64)),
      );
      if (windPin > 0.999) continue;
      const candidates: Sample[] = [];
      for (const group of groups) {
        let nearest:
          { nodes: Triangle; weights: [number, number, number]; distance: number } | undefined;
        for (let i = group.first; i < group.last; i++) {
          const tri = triangles[i];
          const hit = closestWeights(p, nodes[tri[0]], nodes[tri[1]], nodes[tri[2]]);
          if (!nearest || hit.distance < nearest.distance) nearest = { nodes: tri, ...hit };
          if (hit.distance === 0) break;
        }
        if (!nearest) continue;
        // Carry the displacement across the painted silhouette and into transparent
        // space so inverse texture lookup can find the moving free edge.
        const gain = 1 - smooth((nearest.distance - 32) / 80);
        if (gain > 0.001)
          candidates.push({
            offset,
            nodes: nearest.nodes,
            weights: nearest.weights,
            gain,
            projection: group.projection,
          });
      }
      const total = candidates.reduce((sum, sample) => sum + sample.gain, 0);
      if (total) {
        active.push(offset);
        for (const sample of candidates)
          samples.push({ ...sample, gain: (sample.gain * (1 - windPin)) / Math.max(1, total) });
      }
    }
  let simulated = 0,
    angle = 0,
    velocity = 0,
    maxVisibleDisplacement = 0,
    clampedSamples = 0;
  function step(t: number) {
    const dt = 1 / 120,
      wind = walkingWind(t);
    nodes.forEach((n) => {
      if (!n.mass) return;
      const damping = Math.exp(-n.damping * dt);
      const vx = (n.x - n.px) * damping,
        vy = (n.y - n.py) * damping,
        vz = (n.z - n.pz) * damping;
      n.px = n.x;
      n.py = n.y;
      n.pz = n.z;
      n.x += vx - (n.x - n.ox) * 3 * dt * dt;
      n.y += vy - (n.y - n.oy) * 3 * dt * dt;
      n.z += vz + (walkingWind(t - n.delay) * n.force - n.z) * n.response * dt * dt;
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
    offsetsX.fill(0);
    offsetsY.fill(0);
    for (const s of samples) {
      let dx = 0,
        dy = 0;
      for (let i = 0; i < 3; i++) {
        const n = nodes[s.nodes[i]];
        dx += (n.x - n.ox + n.z * s.projection[0]) * s.weights[i];
        dy += (n.y - n.oy + n.z * s.projection[1]) * s.weights[i];
      }
      offsetsX[s.offset / 4] += dx * s.gain;
      offsetsY[s.offset / 4] += dy * s.gain;
    }
    maxVisibleDisplacement = clampedSamples = 0;
    for (const offset of active) {
      const dx = offsetsX[offset / 4],
        dy = offsetsY[offset / 4];
      maxVisibleDisplacement = Math.max(maxVisibleDisplacement, Math.hypot(dx, dy));
      if (Math.abs(dx) > 42 || Math.abs(dy) > 42) clampedSamples++;
      pixels[offset] = Math.round(128 + Math.max(-42, Math.min(42, dx)) * 3);
      pixels[offset + 1] = Math.round(128 + Math.max(-42, Math.min(42, dy)) * 3);
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
        ...nodes.map((n) =>
          Math.hypot(n.x - n.ox + n.z * n.projection[0], n.y - n.oy + n.z * n.projection[1]),
        ),
      ),
      maxVisibleDisplacement,
      clampedSamples,
      angle,
    };
  }
  return { update, metrics };
}
