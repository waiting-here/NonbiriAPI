export interface FaceCalibrationConfig {
  overlayEnabled: boolean;
  overlayOpacity: number;
  freezeMotion: boolean;
  eyeScale: number;
  eyeOffsetY: number;
  eyeGapOffset: number;
  mouthScale: number;
  mouthOffsetY: number;
  faceScaleX: number;
  faceScaleY: number;
  faceOffsetY: number;
  bangsOffsetY: number;
}

export const DEFAULT_FACE_CALIBRATION: FaceCalibrationConfig = {
  overlayEnabled: false,
  overlayOpacity: 0.48,
  freezeMotion: false,
  eyeScale: 1.0,
  eyeOffsetY: 0,
  eyeGapOffset: 0,
  mouthScale: 1.0,
  mouthOffsetY: 0,
  faceScaleX: 1.0,
  faceScaleY: 1.0,
  faceOffsetY: 0,
  bangsOffsetY: 0,
};

const CAL_STORAGE_KEY = 'nonbiri_whale_face_cal_v4';

export function loadFaceCalibration(): FaceCalibrationConfig {
  try {
    const raw = localStorage.getItem(CAL_STORAGE_KEY);
    if (!raw) return { ...DEFAULT_FACE_CALIBRATION };
    const parsed = JSON.parse(raw) as Partial<FaceCalibrationConfig>;
    return {
      ...DEFAULT_FACE_CALIBRATION,
      ...parsed,
      // 每次刷新默认关闭半透明叠底，避免遮挡正常游玩
      overlayEnabled: false,
      freezeMotion: false,
    };
  } catch {
    return { ...DEFAULT_FACE_CALIBRATION };
  }
}

export function saveFaceCalibration(cal: FaceCalibrationConfig): void {
  try {
    localStorage.setItem(CAL_STORAGE_KEY, JSON.stringify(cal));
  } catch {
    // ignore storage quota errors
  }
}
