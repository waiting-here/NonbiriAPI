/** Canonical activity text permits newlines and tabs, with a small UTF-8 bound. */
export function isActivityLiterature(value: unknown): value is string {
  if (typeof value !== 'string') return false;
  const characters = Array.from(value);
  return (
    characters.length <= 1_024 &&
    new TextEncoder().encode(value).byteLength <= 4_096 &&
    characters.every((character) => {
      const point = character.codePointAt(0)!;
      return (
        point === 0x09 ||
        point === 0x0a ||
        (point >= 0x20 && (point < 0x7f || point > 0x9f) && (point < 0xd800 || point > 0xdfff))
      );
    })
  );
}
