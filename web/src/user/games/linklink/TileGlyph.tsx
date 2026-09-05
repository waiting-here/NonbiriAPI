import { useGameCopy } from '../copy';

const ART = [
  <>
    <path d="M24 16C8 5 3 27 16 39q5 4 8 0 4 4 9-1C46 23 39 7 24 16" fill="#ed5264" />
    <path d="M24 17q-2-8 5-11" stroke="#7c5937" strokeWidth="3" fill="none" />
    <path d="M27 13q1-9 12-6-3 8-12 6" fill="#64ab4b" />
  </>,
  <>
    <path d="M19 18q0-7 6-7t7 9c2 6 10 8 9 16-1 12-32 13-33 0 0-8 10-10 11-18" fill="#a2c943" />
    <path d="m24 13 3-8" stroke="#795a36" strokeWidth="3" />
    <path d="M28 12q4-8 13-3-7 7-13 3" fill="#51a466" />
  </>,
  <>
    <path d="M12 29q11-10 12-23 2 16 10 22" fill="none" stroke="#4c9c67" strokeWidth="3" />
    <circle cx="13" cy="32" r="10" fill="#dc405d" />
    <circle cx="35" cy="32" r="10" fill="#f35b6d" />
    <path d="M24 9q7-8 16-2-8 7-16 2" fill="#5dae57" />
  </>,
  <>
    <path d="m24 14 3-9" stroke="#698e3c" strokeWidth="3" />
    <path d="M25 13q2-10 16-5-5 11-16 5" fill="#6baa57" />
    {[
      [17, 18],
      [30, 18],
      [12, 28],
      [24, 29],
      [35, 28],
      [18, 38],
      [29, 38],
      [24, 44],
    ].map(([cx, cy], i) => (
      <circle key={i} cx={cx} cy={cy} r="6" fill={i % 2 ? '#9465ca' : '#754aaf'} />
    ))}
  </>,
  <>
    <path d="M9 8c-1 18 7 29 27 26-8 16-35 11-32-16Z" fill="#f4c74e" />
    <path d="M9 9c1 16 7 24 28 23" fill="none" stroke="#dc9e28" strokeWidth="3" />
    <path d="m7 7 4 2m24 23 5-2" stroke="#80602f" strokeWidth="3" />
  </>,
  <>
    <circle cx="24" cy="28" r="17" fill="#f7943b" />
    <path d="M23 12q4-12 17-7-4 11-17 7" fill="#59a25a" />
    <path d="M13 22q2-5 6-6" stroke="#ffd389" strokeWidth="3" strokeLinecap="round" fill="none" />
  </>,
  <>
    <path d="M4 15h40Q36 46 24 45 10 44 4 15" fill="#4ca870" />
    <path d="M7 15h34Q32 40 24 40 14 39 7 15" fill="#ffe9bf" />
    <path d="M10 15h28Q31 35 24 36 17 35 10 15" fill="#f26979" />
    {[
      [17, 22],
      [29, 22],
      [23, 28],
    ].map(([cx, cy]) => (
      <ellipse key={cx} cx={cx} cy={cy} rx="1.5" ry="2.5" fill="#584452" />
    ))}
  </>,
  <>
    <path d="M7 22q0-13 17-12 18-2 17 12-4 17-17 23Q10 38 7 22" fill="#ec5368" />
    <path d="m10 16 6-9 8 6 8-7 7 10-12 1-4 6-5-7Z" fill="#55a560" />
    {[
      [15, 24],
      [30, 23],
      [23, 30],
      [17, 33],
      [29, 34],
    ].map(([cx, cy], i) => (
      <ellipse key={i} cx={cx} cy={cy} rx="1.2" ry="2" fill="#ffe6a1" />
    ))}
  </>,
  <>
    <path d="m15 17-7-9 12 4 4-11 5 11 12-5-7 12" fill="#56a26d" />
    <rect x="11" y="16" width="27" height="30" rx="12" fill="#eac25c" />
    <path d="m16 20 18 20M12 28l16 17m5-25L15 40m22-12L23 45" stroke="#c89441" strokeWidth="2" />
  </>,
  <>
    <path d="M12 15q10-7 20 7L10 44Z" fill="#f19a4b" />
    <path d="m17 24 7 4m-10 2 5 3" stroke="#db7638" strokeWidth="2" />
    <path
      d="m24 17 1-14m3 17L40 8m-9 12 14-2"
      stroke="#66a765"
      strokeWidth="5"
      strokeLinecap="round"
    />
  </>,
  <>
    <ellipse cx="24" cy="24" rx="10" ry="20" fill="#efc64f" />
    <path d="M19 8v31m10-31v31M15 16h18m-19 8h20m-19 8h18" stroke="#daa23e" strokeWidth="1.6" />
    <path d="M24 45Q5 44 5 17q14 7 19 28M24 45q20-2 20-28Q30 24 24 45" fill="#6da85a" />
  </>,
  <>
    <path d="m18 24-3 18q9 5 18 0l-3-18" fill="#edd9ae" />
    <path d="M3 27C5-4 43-4 45 27Z" fill="#de6570" />
    <circle cx="16" cy="16" r="4" fill="#fff1cf" />
    <circle cx="33" cy="20" r="3" fill="#fff1cf" />
    <path d="M4 27h40" stroke="#a84f59" strokeWidth="2" />
  </>,
  <>
    {Array.from({ length: 8 }, (_, i) => (
      <ellipse
        key={i}
        cx="24"
        cy="10"
        rx="5"
        ry="9"
        fill="#efba46"
        transform={'rotate(' + i * 45 + ' 24 24)'}
      />
    ))}
    <circle cx="24" cy="24" r="10" fill="#9b6845" />
    <circle cx="21" cy="21" r="2" fill="#c38b56" />
  </>,
  <>
    <path
      d="M24 25v20M24 38Q8 41 7 27q11 0 17 11"
      stroke="#55a169"
      strokeWidth="4"
      fill="#74b47b"
    />
    <path d="M9 8q8 0 15 6 7-6 15-6v10q-1 16-15 16T9 18Z" fill="#dc78a3" />
    <path d="m17 15 7-11 7 11-7 14Z" fill="#f296ba" />
  </>,
  <>
    <path
      d="M20 35V10q0-9 8 0v25M20 27H9V15m19 7h11V11"
      stroke="#60a675"
      strokeWidth="7"
      strokeLinejoin="round"
      strokeLinecap="round"
      fill="none"
    />
    <path d="m14 34 3 13h16l3-13Z" fill="#d18a67" />
    <path d="M12 34h26" stroke="#b97659" strokeWidth="4" />
  </>,
  <>
    <path d="M24 27q1 12-9 19" stroke="#508d61" strokeWidth="3" fill="none" />
    {[0, 90, 180, 270].map((angle) => (
      <path
        key={angle}
        d="M24 25Q7 23 10 13q5-8 14-1 10-7 14 1 3 10-14 12"
        fill="#63ac76"
        stroke="#46925f"
        strokeWidth="1"
        transform={'rotate(' + angle + ' 24 25)'}
      />
    ))}
  </>,
  <>
    <path d="m5 10 13 9q16-13 28 5-12 18-28 5L5 39Z" fill="#ee9c58" />
    <path d="m21 16 7-10 7 9m-14 18 7 10 7-10" fill="#e47b42" />
    <circle cx="36" cy="23" r="2.5" fill="#354351" />
    <path d="M17 20v9" stroke="#ffd8a2" strokeWidth="3" />
  </>,
  <>
    <path
      d="M23 21C9-4-4 15 15 27 0 37 13 51 24 30c13 19 27 4 12-5C55 13 37-3 25 21"
      fill="#b08bd5"
    />
    <path d="M22 23Q7 8 10 21l12 5m4-3q15-15 12-2l-12 5" fill="#f0b1c8" />
    <path d="M24 16v19m0-18-5-7m5 7 5-7" stroke="#5d567f" strokeWidth="3" strokeLinecap="round" />
  </>,
  <>
    <circle cx="24" cy="11" r="7" fill="#4b4d59" />
    <ellipse cx="24" cy="29" rx="16" ry="18" fill="#eb6671" />
    <path d="M24 13v33m-8-3-5 3m21-3 5 3M19 7l-4-4m14 4 4-4" stroke="#4b4d59" strokeWidth="2.5" />
    {[
      [15, 23],
      [32, 23],
      [15, 35],
      [33, 35],
    ].map(([cx, cy], i) => (
      <circle key={i} cx={cx} cy={cy} r="3" fill="#4b4d59" />
    ))}
  </>,
  <>
    <ellipse cx="15" cy="15" rx="10" ry="8" fill="#b5e1ee" transform="rotate(30 15 15)" />
    <ellipse cx="32" cy="15" rx="10" ry="8" fill="#b5e1ee" transform="rotate(-30 32 15)" />
    <ellipse cx="24" cy="30" rx="14" ry="16" fill="#efc556" />
    <path d="M11 25h26m-25 11h24" stroke="#806247" strokeWidth="6" />
    <circle cx="24" cy="17" r="7" fill="#806247" />
  </>,
  <>
    <path d="m8 21-2-16 15 9h7L43 5l-3 17q9 24-16 24T8 21" fill="#eca56c" />
    <path d="m9 10 8 7-7 3m29-10-8 7 7 3" fill="#efc2b0" />
    <circle cx="16" cy="28" r="2" fill="#645044" />
    <circle cx="32" cy="28" r="2" fill="#645044" />
    <path d="m21 33 3 3 3-3M7 32l10 2m14 0 10-2" stroke="#645044" strokeWidth="2" fill="none" />
  </>,
  <>
    <path d="M4 4 20 15h8L44 4l-4 25-16 18L8 29Z" fill="#df8857" />
    <path d="m7 10 8 8-7 3m33-11-8 8 7 3" fill="#6d524b" />
    <path d="m8 28 16 6 16-6-16 16Z" fill="#fff0d8" />
    <circle cx="16" cy="25" r="2" fill="#594b44" />
    <circle cx="32" cy="25" r="2" fill="#594b44" />
    <circle cx="24" cy="36" r="3" fill="#594b44" />
  </>,
  <>
    <circle cx="10" cy="11" r="8" fill="#5c6271" />
    <circle cx="38" cy="11" r="8" fill="#5c6271" />
    <circle cx="24" cy="27" r="20" fill="#f1ede1" stroke="#ddd6c9" />
    <ellipse cx="14" cy="25" rx="6" ry="8" fill="#5c6271" transform="rotate(25 14 25)" />
    <ellipse cx="34" cy="25" rx="6" ry="8" fill="#5c6271" transform="rotate(-25 34 25)" />
    <circle cx="16" cy="24" r="2" fill="white" />
    <circle cx="32" cy="24" r="2" fill="white" />
    <path d="m20 34 4 4 4-4Z" fill="#5c6271" />
  </>,
  <>
    <ellipse cx="24" cy="29" rx="21" ry="17" fill="#7fb875" />
    <circle cx="12" cy="14" r="9" fill="#7fb875" />
    <circle cx="36" cy="14" r="9" fill="#7fb875" />
    <circle cx="12" cy="13" r="5" fill="#fff5de" />
    <circle cx="36" cy="13" r="5" fill="#fff5de" />
    <circle cx="13" cy="14" r="2.5" fill="#4a664b" />
    <circle cx="35" cy="14" r="2.5" fill="#4a664b" />
    <path
      d="M13 31q11 10 22 0"
      fill="none"
      stroke="#4a8059"
      strokeWidth="2.5"
      strokeLinecap="round"
    />
  </>,
  <>
    <path
      d="M10 28C-1-5 48-5 38 28l7 10q0 8-8 4l-6-6-1 7q-6 7-10 0l-1-7-6 7q-11 5-8-6Z"
      fill="#bc83b3"
    />
    <circle cx="18" cy="19" r="3" fill="#fff2e4" />
    <circle cx="31" cy="19" r="3" fill="#fff2e4" />
    <circle cx="18" cy="20" r="1.5" fill="#6b4c72" />
    <circle cx="31" cy="20" r="1.5" fill="#6b4c72" />
    <path d="M21 27q3 3 6 0" stroke="#79567d" strokeWidth="2" fill="none" />
  </>,
] as const;

const NAMES = {
  en: [
    'Apple',
    'Pear',
    'Cherries',
    'Grapes',
    'Banana',
    'Orange',
    'Watermelon',
    'Strawberry',
    'Pineapple',
    'Carrot',
    'Corn',
    'Mushroom',
    'Sunflower',
    'Tulip',
    'Cactus',
    'Clover',
    'Fish',
    'Butterfly',
    'Ladybug',
    'Bee',
    'Cat',
    'Fox',
    'Panda',
    'Frog',
    'Octopus',
  ],
  zh: [
    '苹果',
    '梨',
    '樱桃',
    '葡萄',
    '香蕉',
    '橙子',
    '西瓜',
    '草莓',
    '菠萝',
    '胡萝卜',
    '玉米',
    '蘑菇',
    '向日葵',
    '郁金香',
    '仙人掌',
    '四叶草',
    '小鱼',
    '蝴蝶',
    '瓢虫',
    '蜜蜂',
    '猫咪',
    '狐狸',
    '熊猫',
    '青蛙',
    '章鱼',
  ],
} as const;

export function TileGlyph({ tileKey }: { readonly tileKey: string }) {
  const { language } = useGameCopy();
  const index = Number(tileKey.slice(5)) - 1;
  return (
    <svg
      viewBox="0 0 48 48"
      role="img"
      aria-label={NAMES[language === 'zh' ? 'zh' : 'en'][index]}
      focusable="false"
    >
      {ART[index]}
    </svg>
  );
}
