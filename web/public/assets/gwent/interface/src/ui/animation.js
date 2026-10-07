/* eslint-disable */
let audioContext;
export function tone(kind = 'play') {
  if (localStorage.getItem('arena-sound') !== 'on') return;
  try {
    audioContext ||= new (window.AudioContext || window.webkitAudioContext)();
    audioContext.resume();
    const oscillator = audioContext.createOscillator(),
      gain = audioContext.createGain();
    oscillator.type = 'sine';
    oscillator.frequency.setValueAtTime(
      kind === 'win' ? 660 : kind === 'scorch' ? 130 : 330,
      audioContext.currentTime,
    );
    gain.gain.setValueAtTime(0.035, audioContext.currentTime);
    gain.gain.exponentialRampToValueAtTime(0.001, audioContext.currentTime + 0.18);
    oscillator.connect(gain);
    gain.connect(audioContext.destination);
    oscillator.start();
    oscillator.stop(audioContext.currentTime + 0.2);
  } catch {
    /* Sound must never block a match. */
  }
}
export function motionAllowed() {
  return (
    document.documentElement.dataset.motion !== 'reduced' &&
    !matchMedia('(prefers-reduced-motion: reduce)').matches
  );
}
export function pulse(element, { kind = 'effect', speed = 0.5 } = {}) {
  if (!element || !motionAllowed()) return;
  const frames =
    kind === 'play'
      ? [
          { opacity: 0.3, transform: 'translateY(12px) scale(.94)' },
          { opacity: 1, transform: 'none' },
        ]
      : kind === 'scorch'
        ? [
            { filter: 'brightness(1)', opacity: 1 },
            { filter: 'brightness(2) sepia(1)', opacity: 0.35 },
          ]
        : [{ filter: 'brightness(1.8)' }, { filter: 'brightness(1)' }];
  element.getAnimations().forEach((animation) => animation.cancel());
  return element.animate(frames, {
    duration: Math.max(40, 640 * speed),
    easing: 'ease-out',
  });
}

export function suspendSound() {
  audioContext?.suspend();
}
export function closeSound() {
  audioContext?.close();
  audioContext = null;
}
