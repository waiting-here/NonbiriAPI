export interface FormattedError {
  text?: string;
  reason?: 'invalid' | 'depth' | 'size' | 'truncated';
}

// Called in a worker. JSON is validated without evaluating HTML or scripts,
// and indentation has an independent output budget.
export function formatErrorJSON(raw: string, truncated: boolean): FormattedError {
  if (truncated) return { reason: 'truncated' };
  if (raw.length > 1_048_576) return { reason: 'size' };
  let depth = 0;
  let quoted = false;
  let escaped = false;
  for (const char of raw) {
    if (quoted) {
      if (escaped) escaped = false;
      else if (char === '\\') escaped = true;
      else if (char === '"') quoted = false;
    } else if (char === '"') quoted = true;
    else if (char === '{' || char === '[') {
      if (++depth > 64) return { reason: 'depth' };
    } else if (char === '}' || char === ']') depth--;
  }
  try {
    JSON.parse(raw);
  } catch {
    return { reason: 'invalid' };
  }
  const parts: string[] = [];
  let size = 0;
  depth = 0;
  quoted = false;
  escaped = false;
  for (const char of raw) {
    let part = char;
    if (quoted) {
      if (escaped) escaped = false;
      else if (char === '\\') escaped = true;
      else if (char === '"') quoted = false;
    } else if (char === '"') quoted = true;
    else if (/\s/.test(char)) continue;
    else if (char === '{' || char === '[') {
      depth++;
      part += `\n${'  '.repeat(depth)}`;
    } else if (char === '}' || char === ']') {
      depth--;
      part = `\n${'  '.repeat(depth)}${char}`;
    } else if (char === ',') part += `\n${'  '.repeat(depth)}`;
    else if (char === ':') part += ' ';
    size += part.length;
    if (size > 2_097_152) return { reason: 'size' };
    parts.push(part);
  }
  return { text: parts.join('') };
}
