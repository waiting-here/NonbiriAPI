// A shared, lazily installed signal for "the user is typing or scrolling".
// Ambient backgrounds paint less often during that window so input stays smooth.
const BUSY_MS = 600;
let busyUntil = 0;
let installed = false;

function mark() {
  busyUntil = performance.now() + BUSY_MS;
}

function install() {
  if (installed || typeof window === 'undefined') return;
  installed = true;
  const options = { capture: true, passive: true } as const;
  for (const type of ['scroll', 'wheel', 'touchmove', 'keydown', 'input'])
    window.addEventListener(type, mark, options);
}

export function userBusy(now: number): boolean {
  install();
  return now < busyUntil;
}
