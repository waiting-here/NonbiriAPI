/* eslint-disable */
const speeds = [0.5, 0.12];
export function loadPreferences(storage) {
  let speed = 0.5,
    motion = 'full';
  try {
    const savedSpeed = Number(storage.getItem('arena-speed'));
    if (speeds.includes(savedSpeed)) speed = savedSpeed;
    if (storage.getItem('arena-motion') === 'reduced') motion = 'reduced';
  } catch {
    /* Defaults remain usable if storage is unavailable. */
  }
  return { speed, motion };
}
export function applyMotion(motion) {
  document.documentElement.dataset.motion = motion;
  if (motion === 'reduced') document.getAnimations().forEach((animation) => animation.cancel());
}
export function savePreference(key, value) {
  try {
    localStorage.setItem(`arena-${key}`, String(value));
  } catch {
    /* Settings still apply for this session. */
  }
}
