const COLORS = ['#2f80ed', '#9b51e0', '#eb5757', '#27ae60', '#f2994a'] as const;

export function TileGlyph({ tileKey }: { readonly tileKey: string }) {
  const index = Number(tileKey.slice(5));
  const color = COLORS[index % COLORS.length];
  const variant = index % 5;
  return (
    <svg viewBox="0 0 48 48" aria-hidden="true" focusable="false">
      <circle cx="24" cy="24" r="19" fill={color} opacity=".14" />
      {variant === 0 ? <path d="M13 27 24 10l11 17-11 11Z" fill={color} /> : null}
      {variant === 1 ? (
        <path d="M12 16h24v18H12zM18 10v28M30 10v28" fill="none" stroke={color} strokeWidth="4" />
      ) : null}
      {variant === 2 ? (
        <path d="m24 9 4.5 10 10.5 1-8 7 2.5 11L24 32l-9.5 6L17 27l-8-7 10.5-1Z" fill={color} />
      ) : null}
      {variant === 3 ? (
        <path
          d="M10 25c7-13 21-13 28 0-7 13-21 13-28 0Zm14-7v14"
          fill="none"
          stroke={color}
          strokeWidth="4"
        />
      ) : null}
      {variant === 4 ? (
        <path d="M12 12h10v10H12zm14 0h10v10H26zM12 26h10v10H12zm14 0h10v10H26z" fill={color} />
      ) : null}
      <text x="24" y="44" textAnchor="middle" fontSize="7" fill="currentColor">
        {index}
      </text>
    </svg>
  );
}
