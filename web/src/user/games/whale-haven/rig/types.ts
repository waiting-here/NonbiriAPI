/**
 * 2D 分层骨骼与插槽换装契约 (Layered 2D Skeletal & Slot Rig Contract)
 * 供生图资产管线 (Codex) 与前端骨骼物理渲染引擎共用。
 */

export type BoneId =
  | 'root'
  | 'torso'
  | 'head'
  | 'ahoge'
  | 'ear_l'
  | 'ear_r'
  | 'hair_back'
  | 'hair_side_l'
  | 'hair_side_r'
  | 'arm_l'
  | 'arm_r'
  | 'hand_prop'
  | 'leg_l'
  | 'leg_r'
  | 'tail_base'
  | 'tail_tip';

export type SlotId =
  | 'tail'
  | 'hair_back'
  | 'leg_l'
  | 'leg_r'
  | 'torso_outfit'
  | 'arm_l'
  | 'arm_r'
  | 'hand_prop'
  | 'head_base'
  | 'ear_fin_l'
  | 'ear_fin_r'
  | 'blush'
  | 'eyes_iris'
  | 'eyes_highlight'
  | 'eyelids'
  | 'mouth'
  | 'face_fx'
  | 'brows'
  | 'hair_front'
  | 'hair_side_l'
  | 'hair_side_r'
  | 'headwear'
  | 'ahoge';

export interface SpringPhysicsConfig {
  /** 刚度系数 (推荐范围 60 ~ 180，呆毛偏高、长发与大尾巴偏柔) */
  readonly stiffness: number;
  /** 阻尼系数 (推荐范围 8 ~ 22，水下环境可略高以体现海水浮力感) */
  readonly damping: number;
  /** 最大摆动角度限制 (度) */
  readonly maxAngleDeg: number;
}

export interface BoneDefinition {
  readonly id: BoneId;
  readonly parent: BoneId | null;
  /** 在 1024x1024 标准母版画布上的关节旋转枢轴坐标 (px) */
  readonly pivotX: number;
  readonly pivotY: number;
  /** 可选二阶弹簧物理跟随配置 */
  readonly spring?: SpringPhysicsConfig;
}

export interface LayerAssetEntry {
  /** 相对 public/rig/whale-maid/ 的 WebP 或 PNG 路径 */
  readonly file: string;
  /**
   * 若导出为全尺寸 1024x1024 透明画布，offset 为 (0, 0), width=1024, height=1024；
   * 若导出为裁剪包围盒，则记录其在 1024x1024 母版中的左上角偏移与尺寸。
   */
  readonly offsetX: number;
  readonly offsetY: number;
  readonly width: number;
  readonly height: number;
}

export interface SlotDefinition {
  readonly id: SlotId;
  readonly bone: BoneId;
  /** 渲染层级：数值越小越靠后，数值越大越靠前 */
  readonly zIndex: number;
  readonly defaultVariant: string;
  readonly variants: Record<string, LayerAssetEntry>;
}

export interface OutfitPreset {
  readonly id: string;
  readonly name: string;
  readonly description: string;
  /** 穿戴该服装时覆盖的插槽变体映射 */
  readonly slotOverrides: Partial<Record<SlotId, string>>;
}

export interface ExpressionPreset {
  readonly id: string;
  readonly name: string;
  readonly slotOverrides: Partial<Record<SlotId, string>>;
}

export interface WhaleRigManifest {
  readonly schemaVersion: 1;
  readonly canvasWidth: 1024;
  readonly canvasHeight: 1024;
  /** 角色脚底接地点 Y 坐标 */
  readonly groundY: number;
  readonly bones: readonly BoneDefinition[];
  readonly slots: readonly SlotDefinition[];
  readonly outfits: readonly OutfitPreset[];
  readonly expressions: readonly ExpressionPreset[];
}
