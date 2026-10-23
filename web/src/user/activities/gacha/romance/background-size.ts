export function backgroundRenderSize(width: number, height: number, projectedScale: number) {
  const outputWidth = Math.min(width, Math.max(1536, Math.ceil(1536 * projectedScale)));
  return [outputWidth, Math.round((outputWidth * height) / width)] as const;
}
