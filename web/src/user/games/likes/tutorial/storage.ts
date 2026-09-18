const key = 'nonbiri.game.likes.tutorial.v1';
let memory: 'completed' | 'skipped' | null = null;
export function tutorialStatus() {
  try {
    const saved = localStorage.getItem(key);
    if (saved === 'completed' || saved === 'skipped') return saved;
  } catch {
    /* The walkthrough also works without browser storage. */
  }
  return memory;
}
export function saveTutorialStatus(status: 'completed' | 'skipped') {
  memory = status;
  try {
    localStorage.setItem(key, status);
  } catch {
    /* Keep the page-local choice. */
  }
}
export const tutorialStorageKey = key;
