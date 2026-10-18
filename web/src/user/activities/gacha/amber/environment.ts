// Source rectangles are registered independently of their anchors in the hall.
// Each banner bends below a fixed top; every chandelier uses one rigid transform.
export const bannerLayers = [
  { source: [0.123, 0, 0.094, 0.414], target: [0.194, 0.035, 0.061, 0.279] },
  { source: [0.201, 0.293, 0.065, 0.432], target: [0.248, 0.249, 0.039, 0.279] },
  { source: [0.273, 0.462, 0.048, 0.348], target: [0.297, 0.351, 0.028, 0.231] },
  { source: [0.796, 0, 0.093, 0.414], target: [0.79, 0.026, 0.061, 0.291] },
  { source: [0.733, 0.291, 0.065, 0.434], target: [0.735, 0.244, 0.039, 0.282] },
  { source: [0.678, 0.462, 0.05, 0.348], target: [0.683, 0.351, 0.028, 0.23] },
  { source: [0.629, 0.589, 0.041, 0.306], target: [0.636, 0.471, 0.026, 0.172] },
] as const;

export const chandelierLayers = [
  { source: [0.262, 0, 0.181, 0.296], target: [0.298, -0.008, 0.082, 0.164] },
  { source: [0.559, 0, 0.181, 0.296], target: [0.611, -0.008, 0.082, 0.164] },
  { source: [0.243, 0.314, 0.132, 0.315], target: [0.297, 0.185, 0.068, 0.17] },
  { source: [0.627, 0.314, 0.132, 0.315], target: [0.603, 0.185, 0.068, 0.17] },
  { source: [0.275, 0.642, 0.09, 0.204], target: [0.326, 0.333, 0.047, 0.12] },
  { source: [0.632, 0.642, 0.09, 0.204], target: [0.628, 0.333, 0.047, 0.12] },
] as const;

const vec = (values: readonly number[]) => `vec4(${values.map((v) => v.toFixed(6)).join(',')})`;
export const environmentLayers = [
  ...bannerLayers.map(
    (v, i) => `color=over(color,banner(p,${vec(v.source)},${vec(v.target)},phase,${i}.));`,
  ),
  ...chandelierLayers.map(
    (v, i) => `color=over(color,chandelier(p,${vec(v.source)},${vec(v.target)},phase,${i}.));`,
  ),
].join('\n');
