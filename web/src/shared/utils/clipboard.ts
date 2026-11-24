// Clipboard helper with a graceful fallback. The async Clipboard API is
// preferred; when it is unavailable (older browsers, non-secure contexts) a
// hidden textarea + execCommand fallback runs instead. The return value only
// reports whether the copy likely succeeded so the caller can show feedback.

export async function copyText(
  value: string,
  options: { fallbackOnRejection?: boolean } = {},
): Promise<boolean> {
  if (!value) return false;
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(value);
      return true;
    }
  } catch {
    if (options.fallbackOnRejection === false) return false;
  }
  try {
    const textarea = document.createElement('textarea');
    textarea.value = value;
    textarea.setAttribute('readonly', '');
    textarea.style.position = 'fixed';
    textarea.style.opacity = '0';
    document.body.appendChild(textarea);
    try {
      textarea.select();
      return document.execCommand('copy');
    } finally {
      textarea.remove();
    }
  } catch {
    return false;
  }
}
