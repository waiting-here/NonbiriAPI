export type GameHeroKind = 'fishing' | 'linklink' | 'rps';

export function GameHero({ kind }: { kind: GameHeroKind }) {
  if (kind === 'fishing') {
    return (
      <svg className="game-hero" viewBox="0 0 480 240" aria-hidden="true" focusable="false">
        <defs>
          <linearGradient id="pond-sky" x1="0" y1="0" x2="0" y2="1">
            <stop stopColor="currentColor" stopOpacity=".08" />
            <stop offset="1" stopColor="currentColor" stopOpacity=".22" />
          </linearGradient>
          <linearGradient id="pond-water" x1="0" y1="0" x2="1" y2="1">
            <stop stopColor="currentColor" stopOpacity=".34" />
            <stop offset="1" stopColor="currentColor" stopOpacity=".1" />
          </linearGradient>
        </defs>
        <path fill="url(#pond-sky)" d="M0 0h480v150H0z" />
        <circle cx="386" cy="54" r="29" fill="currentColor" opacity=".2" />
        <path fill="url(#pond-water)" d="M0 137q85-20 166 2t167-1 147 3v99H0z" />
        <path
          d="M42 169q72-18 144 0t144 0 110 0M21 198q59-13 119 0t119 0 105 0"
          fill="none"
          stroke="currentColor"
          strokeOpacity=".3"
          strokeWidth="3"
        />
        <path
          d="M120 30q84 20 140 111"
          fill="none"
          stroke="currentColor"
          strokeWidth="8"
          strokeLinecap="round"
        />
        <path d="M258 140v51" fill="none" stroke="currentColor" strokeWidth="2" />
        <path d="M248 192q10-16 20 0-10 12-20 0Z" fill="currentColor" />
        <g transform="translate(322 178)" fill="none" stroke="currentColor" strokeWidth="4">
          <path d="M-45 0q33-31 76 0-43 31-76 0Z" />
          <path d="m31 0 22-19v38Z" />
          <circle cx="-23" cy="-6" r="2" fill="currentColor" />
        </g>
      </svg>
    );
  }
  if (kind === 'linklink') {
    return (
      <svg className="game-hero" viewBox="0 0 480 240" aria-hidden="true" focusable="false">
        <defs>
          <pattern id="link-grid" width="44" height="44" patternUnits="userSpaceOnUse">
            <rect x="3" y="3" width="36" height="36" rx="10" fill="currentColor" opacity=".1" />
          </pattern>
        </defs>
        <rect width="480" height="240" fill="url(#link-grid)" />
        <g fill="currentColor">
          <circle cx="102" cy="69" r="18" opacity=".72" />
          <circle cx="366" cy="170" r="18" opacity=".72" />
          <path opacity=".2" d="M190 30h38v38h-38zM278 74h38v38h-38zM102 154h38v38h-38z" />
        </g>
        <path
          d="M102 69H212v101h154"
          fill="none"
          stroke="currentColor"
          strokeWidth="9"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
        <path
          d="m346 151 20 19-20 19"
          fill="none"
          stroke="currentColor"
          strokeWidth="7"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
    );
  }
  return (
    <svg className="game-hero" viewBox="0 0 480 240" aria-hidden="true" focusable="false">
      <circle cx="240" cy="118" r="91" fill="currentColor" opacity=".08" />
      <path
        d="M240 25 95 206h290Z"
        fill="none"
        stroke="currentColor"
        strokeOpacity=".18"
        strokeWidth="6"
      />
      <g transform="translate(240 58)">
        <circle r="38" fill="currentColor" opacity=".18" />
        <path d="m-13 8 2-28 12 18 12-18 2 28-15 12Z" fill="currentColor" />
      </g>
      <g transform="translate(120 180)">
        <circle r="38" fill="currentColor" opacity=".18" />
        <path d="M-18 7q0-24 18-24T18 7v15h-36Z" fill="currentColor" />
      </g>
      <g transform="translate(360 180)">
        <circle r="38" fill="currentColor" opacity=".18" />
        <path d="M-22 17 0-23l22 40-22-8Z" fill="currentColor" />
      </g>
      <path
        d="M217 92 144 155m119-63 73 63M166 180h148"
        fill="none"
        stroke="currentColor"
        strokeWidth="5"
        strokeDasharray="9 10"
        opacity=".55"
      />
    </svg>
  );
}
