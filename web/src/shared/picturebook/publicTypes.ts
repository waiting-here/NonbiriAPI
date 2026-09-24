export const parameterKeys = [
  'prompt',
  'negative_prompt',
  'n',
  'size',
  'aspect_ratio',
  'resolution',
  'seed',
  'steps',
  'guidance',
  'quality',
] as const;
export type ParameterKey = (typeof parameterKeys)[number];
export type Scalar = string | number;
export type LengthUnit = 'utf8_bytes' | 'unicode_scalars' | 'utf16_units';
export interface DimensionAxis {
  minimum: number;
  maximum: number;
  step: number;
}
export interface SizeDimensions {
  format: 'width_height';
  width: DimensionAxis;
  height: DimensionAxis;
}
export interface ParameterRule {
  key: ParameterKey;
  supported: boolean;
  required: boolean;
  type: 'string' | 'integer' | 'number';
  minimum?: number;
  maximum?: number;
  step?: number;
  enum?: Scalar[];
  default?: Scalar;
  min_length?: number;
  max_length?: number;
  length_unit?: LengthUnit;
  dimensions?: SizeDimensions;
}
export interface CombinationRule {
  keys: ParameterKey[];
  allowed: (Scalar | null)[][];
}
export interface Price {
  paper: string;
  brush: string;
}
export interface ImageModel {
  id: string;
  display_name: string;
  description: string;
  revision: string;
  price: Price;
  parameters: ParameterRule[];
  combinations: CombinationRule[];
}
export type SubmitInput = {
  model_id: string;
  expected_model_revision: string;
  prompt: string;
  negative_prompt?: string;
  n?: number;
  size?: Scalar;
  aspect_ratio?: Scalar;
  resolution?: Scalar;
  seed?: Scalar;
  steps?: Scalar;
  guidance?: Scalar;
  quality?: Scalar;
};
export const taskStatuses = [
  'queued',
  'dispatching',
  'running',
  'succeeded',
  'failed',
  'cancelled',
  'unknown_refunded',
] as const;
export type TaskStatus = (typeof taskStatuses)[number];
export interface ImageInfo {
  index: number;
  mime: 'image/png' | 'image/jpeg' | 'image/webp';
  bytes: number;
}
export interface ImageTask {
  id: string;
  model_id: string;
  status: TaskStatus;
  billing_state: 'reserved' | 'charged' | 'refunded';
  n: number;
  actual_images: number;
  created_at: number;
  dispatched_at: number | null;
  completed_at: number | null;
  charge: Price;
  refund: Price;
  queue_position: number | null;
  result_expires_at: number | null;
  result_available: boolean;
  error_code: string | null;
  images: ImageInfo[];
}
export interface ImageQueue {
  queued: number;
  running: number;
  own: { task_id: string; position: number | null; accepted_at: number }[];
  dispatch_paused: boolean;
}
export interface ImagePage<T> {
  data: T[];
  next_cursor: string | null;
}
export const activeTask = (task: ImageTask) =>
  ['queued', 'dispatching', 'running'].includes(task.status);
