/* global document, window, requestAnimationFrame, cancelAnimationFrame, Node */
export function mountLake(root, bridge) {
  const lifetime = new AbortController();
  const on = (target, event, handler) =>
    target.addEventListener(event, handler, { signal: lifetime.signal });
  const localize = (value) =>
    bridge.language === 'en' ? bridge.translate(String(value)) : String(value);
  const textDescriptor = Object.getOwnPropertyDescriptor(Node.prototype, 'textContent');
  const localized = (element) => {
    if (!element || Object.hasOwn(element, 'textContent')) return element;
    Object.defineProperty(element, 'textContent', {
      get() {
        return textDescriptor.get.call(this);
      },
      set(value) {
        textDescriptor.set.call(this, localize(value));
      },
    });
    const setAttribute = element.setAttribute;
    element.setAttribute = function (name, value) {
      setAttribute.call(
        this,
        name,
        ['aria-label', 'aria-valuetext', 'title', 'alt'].includes(name) ? localize(value) : value,
      );
    };
    for (const attr of ['title', 'alt'])
      if (attr in element)
        Object.defineProperty(element, attr, {
          get() {
            return this.getAttribute(attr) ?? '';
          },
          set(value) {
            this.setAttribute(attr, value);
          },
        });
    return element;
  };
  const createElement = (tag) => localized(document.createElement(tag));
  if (bridge.language === 'en') {
    const visit = (node) => {
      if (node.nodeType === 3) node.textContent = localize(node.textContent);
      else {
        if (node.nodeType === 1)
          for (const attr of ['aria-label', 'aria-valuetext', 'title', 'alt'])
            if (node.hasAttribute(attr)) node.setAttribute(attr, localize(node.getAttribute(attr)));
        for (const child of node.childNodes) visit(child);
      }
    };
    visit(root);
  }
  const presentProfile = (p) => {
    const profile = structuredClone(p);
    for (const key of ['trashRecovered', 'treasureOpened', 'completedContracts', 'streak'])
      profile[key] = Number(profile[key]);
    const names = ['普通', '银星', '金星', '铱星'];
    for (const entry of profile.basket) entry.quality = names[entry.quality];
    for (const entry of Object.values(profile.records)) {
      entry.caught = Number(entry.caught);
      entry.perfectCount = Number(entry.perfectCount);
      entry.bestQuality = names[entry.bestQuality];
    }
    return profile;
  };

  const CONFIG = Object.freeze({
    gravity: (0.25 * 60 * 60) / 568,
    holdAcceleration: (-0.5 * 60 * 60) / 568,
    maxSpeed: 2.4,
    bottomBounce: 2 / 3,
    baseBarHeight: 96 / 568,
    levelBarStep: 4 / 568,
    maxBarHeight: 0.5,
    maxLevel: 20,
    initialProgress: 0.3,
    progressGainPerSecond: 0.12,
    progressLossPerSecond: 0.15,
    bitePreparationSeconds: 0.5,
    gentleDartReverseSeconds: 0.65,
    commonDartDistance: 120,
    uncommonDartDistance: 145,
    perfectAccuracy: 0.85,
    perfectMaxMissSeconds: 0.6,
    perfectXpMultiplier: 2.4,
    fixedStep: 1 / 60,
    trashChance: 0.08,
    treasureChance: 0.15,
    treasureDelay: 2.2,
    treasureCaptureSeconds: 2,
  });
  const RARITIES = Object.freeze({
    common: { label: '常见', rank: 1 },
    uncommon: { label: '少见', rank: 2 },
    rare: { label: '稀有', rank: 3 },
    epic: { label: '史诗', rank: 4 },
    legendary: { label: '传说', rank: 5 },
  });
  const DEFAULT_CHALLENGE = Object.freeze({
    difficulty: 20,
    fishSpeed: 1,
    tempo: 1,
    gain: 1,
    loss: 1,
  });
  // Share each tier's budget among fish currently eligible in this water.
  const LOCATIONS = Object.freeze({
    lake: {
      name: '湖泊',
      scene: '湖面很安静，适合钓鱼。',
      description: '宽阔平静的淡水，从小型鲫鱼到巨大的青鱼。',
    },
    river: {
      name: '河流',
      scene: '水流穿过石滩，鱼儿藏在急缓交界处。',
      description: '水流多变的河道，游速快的溪鱼与洄游鱼更多。',
    },
    coast: {
      name: '海岸',
      scene: '海风吹过栈桥，浪花轻拍岸边。',
      description: '咸水栈桥与近海，既有成群的小鱼，也有远游的大鱼。',
    },
    jungle: {
      name: '丛林河湾',
      scene: '树影落在河湾，热带鱼儿穿梭在浅滩与树根之间。',
      description: '密林环抱的热带河湾，小鱼聚在浅水，大型鱼藏在弯道深处。',
    },
  });
  const FISH_TYPES = Object.freeze([
    {
      name: '鲫鱼',
      kind: 'gold',
      location: 'lake',
      rarity: 'common',
      weight: 24,
      behavior: 'smooth',
      style: '平稳型',
      length: [15, 35],
      difficulty: 15,
      basePrice: 22,
    },
    {
      name: '鲤鱼',
      kind: 'lake_carp',
      location: 'lake',
      rarity: 'common',
      weight: 19,
      behavior: 'mixed',
      style: '混合型',
      length: [25, 75],
      difficulty: 33,
      basePrice: 32,
    },
    {
      name: '黄颡鱼',
      kind: 'lake_yellowcat',
      location: 'lake',
      rarity: 'common',
      weight: 17,
      behavior: 'sinker',
      style: '下沉型',
      length: [14, 32],
      difficulty: 25,
      basePrice: 28,
    },
    {
      name: '鳊鱼',
      kind: 'sky',
      location: 'lake',
      rarity: 'uncommon',
      weight: 12,
      behavior: 'floater',
      style: '上浮型',
      length: [22, 52],
      difficulty: 55,
      basePrice: 50,
    },
    {
      name: '草鱼',
      kind: 'lake_grasscarp',
      location: 'lake',
      rarity: 'uncommon',
      weight: 11,
      behavior: 'smooth',
      style: '平稳型',
      length: [40, 100],
      difficulty: 48,
      basePrice: 55,
    },
    {
      name: '鲶鱼',
      kind: 'mud',
      location: 'lake',
      rarity: 'uncommon',
      weight: 10,
      behavior: 'sinker',
      style: '下沉型',
      length: [35, 85],
      difficulty: 50,
      basePrice: 46,
    },
    {
      name: '大口黑鲈',
      kind: 'silver',
      location: 'lake',
      rarity: 'rare',
      weight: 8,
      behavior: 'mixed',
      style: '混合型',
      length: [28, 58],
      difficulty: 62,
      basePrice: 75,
    },
    {
      name: '鳜鱼',
      kind: 'violet',
      location: 'lake',
      rarity: 'rare',
      weight: 6,
      behavior: 'dart',
      style: '乱窜型',
      length: [42, 92],
      difficulty: 72,
      basePrice: 90,
    },
    {
      name: '乌鳢',
      kind: 'lake_snakehead',
      location: 'lake',
      rarity: 'rare',
      weight: 6,
      behavior: 'sinker',
      style: '下沉型',
      length: [35, 90],
      difficulty: 69,
      basePrice: 86,
    },
    {
      name: '白鲢',
      kind: 'lake_silvercarp',
      location: 'lake',
      rarity: 'epic',
      weight: 3,
      behavior: 'floater',
      style: '上浮型',
      length: [60, 120],
      difficulty: 77,
      basePrice: 130,
    },
    {
      name: '鳙鱼',
      kind: 'lake_bighead',
      location: 'lake',
      rarity: 'epic',
      weight: 3,
      behavior: 'mixed',
      style: '混合型',
      length: [65, 135],
      difficulty: 83,
      basePrice: 150,
    },
    {
      name: '青鱼',
      kind: 'star',
      location: 'lake',
      rarity: 'legendary',
      weight: 1.5,
      behavior: 'smooth',
      style: '平稳型',
      length: [70, 140],
      difficulty: 95,
      basePrice: 320,
    },
    {
      name: '鳑鲏',
      kind: 'lake_bitterling',
      location: 'lake',
      rarity: 'common',
      weight: 12,
      behavior: 'smooth',
      style: '平稳型',
      length: [5, 12],
      difficulty: 18,
      basePrice: 22,
      periods: ['dawn', 'day'],
      description: '体侧泛着粉蓝光泽，喜欢在浅水的水草间穿梭。',
    },
    {
      name: '银鱼',
      kind: 'lake_icefish',
      location: 'lake',
      rarity: 'uncommon',
      weight: 8,
      behavior: 'floater',
      style: '上浮型',
      length: [8, 20],
      difficulty: 38,
      basePrice: 45,
      periods: ['dawn', 'dusk'],
      description: '半透明的小鱼成群游动，在斜照的天光下才容易看见。',
    },
    {
      name: '丁鱥',
      kind: 'lake_tench',
      location: 'lake',
      rarity: 'uncommon',
      weight: 8,
      behavior: 'sinker',
      style: '下沉型',
      length: [20, 55],
      difficulty: 43,
      basePrice: 52,
      weathers: ['cloudy', 'rain'],
      description: '橄榄色的身体贴着湖底缓行，阴暗天气更愿意觅食。',
    },
    {
      name: '翘嘴鲌',
      kind: 'lake_topmouth',
      location: 'lake',
      rarity: 'rare',
      weight: 5,
      behavior: 'dart',
      style: '乱窜型',
      length: [35, 85],
      difficulty: 67,
      basePrice: 88,
      periods: ['dawn', 'dusk'],
      description: '上翘的嘴适合追逐水面小鱼，出击时快得像一道银光。',
    },
    {
      name: '黄鳝',
      kind: 'lake_swamp_eel',
      location: 'lake',
      rarity: 'rare',
      weight: 5,
      behavior: 'sinker',
      style: '下沉型',
      length: [35, 90],
      difficulty: 57,
      basePrice: 78,
      periods: ['night'],
      description: '夜色降临后离开泥洞，沿着水草边缘寻找猎物。',
    },
    {
      name: '鲟鱼',
      kind: 'lake_sturgeon',
      location: 'lake',
      rarity: 'epic',
      weight: 2,
      behavior: 'mixed',
      style: '混合型',
      length: [80, 165],
      difficulty: 84,
      basePrice: 185,
      periods: ['dawn', 'night'],
      weathers: ['rain'],
      description: '古老的硬鳞鱼，雨水搅动湖底时才偶尔靠近岸边。',
    },
    {
      name: '马口鱼',
      kind: 'teal',
      location: 'river',
      rarity: 'common',
      weight: 23,
      behavior: 'mixed',
      style: '混合型',
      length: [10, 28],
      difficulty: 28,
      basePrice: 26,
    },
    {
      name: '宽鳍鱲',
      kind: 'river_zacco',
      location: 'river',
      rarity: 'common',
      weight: 19,
      behavior: 'dart',
      style: '乱窜型',
      length: [9, 22],
      difficulty: 35,
      basePrice: 30,
    },
    {
      name: '泥鳅',
      kind: 'river_loach',
      location: 'river',
      rarity: 'common',
      weight: 18,
      behavior: 'sinker',
      style: '下沉型',
      length: [10, 24],
      difficulty: 20,
      basePrice: 24,
    },
    {
      name: '棒花鱼',
      kind: 'river_gudgeon',
      location: 'river',
      rarity: 'common',
      weight: 15,
      behavior: 'smooth',
      style: '平稳型',
      length: [8, 18],
      difficulty: 24,
      basePrice: 25,
    },
    {
      name: '香鱼',
      kind: 'river_ayu',
      location: 'river',
      rarity: 'uncommon',
      weight: 12,
      behavior: 'floater',
      style: '上浮型',
      length: [16, 34],
      difficulty: 48,
      basePrice: 48,
    },
    {
      name: '河鲈',
      kind: 'river_perch',
      location: 'river',
      rarity: 'uncommon',
      weight: 11,
      behavior: 'mixed',
      style: '混合型',
      length: [18, 48],
      difficulty: 52,
      basePrice: 51,
    },
    {
      name: '虹鳟',
      kind: 'red',
      location: 'river',
      rarity: 'rare',
      weight: 8,
      behavior: 'dart',
      style: '乱窜型',
      length: [26, 62],
      difficulty: 60,
      basePrice: 72,
    },
    {
      name: '褐鳟',
      kind: 'river_browntrout',
      location: 'river',
      rarity: 'rare',
      weight: 7,
      behavior: 'mixed',
      style: '混合型',
      length: [30, 76],
      difficulty: 68,
      basePrice: 84,
    },
    {
      name: '狗鱼',
      kind: 'river_pike',
      location: 'river',
      rarity: 'rare',
      weight: 5,
      behavior: 'dart',
      style: '乱窜型',
      length: [45, 110],
      difficulty: 75,
      basePrice: 100,
    },
    {
      name: '欧洲鳗',
      kind: 'frost',
      location: 'river',
      rarity: 'epic',
      weight: 3,
      behavior: 'sinker',
      style: '下沉型',
      length: [65, 130],
      difficulty: 75,
      basePrice: 128,
    },
    {
      name: '大西洋鲑',
      kind: 'river_salmon',
      location: 'river',
      rarity: 'epic',
      weight: 3,
      behavior: 'mixed',
      style: '混合型',
      length: [55, 125],
      difficulty: 82,
      basePrice: 155,
    },
    {
      name: '鳡鱼',
      kind: 'abyss',
      location: 'river',
      rarity: 'legendary',
      weight: 1.5,
      behavior: 'dart',
      style: '乱窜型',
      length: [110, 180],
      difficulty: 95,
      basePrice: 320,
    },
    {
      name: '花鳅',
      kind: 'river_spined_loach',
      location: 'river',
      rarity: 'common',
      weight: 11,
      behavior: 'sinker',
      style: '下沉型',
      length: [8, 18],
      difficulty: 24,
      basePrice: 27,
      periods: ['dusk', 'night'],
      description: '小巧的花纹鱼，贴着河床的石缝和砂砾穿行。',
    },
    {
      name: '茴鱼',
      kind: 'river_grayling',
      location: 'river',
      rarity: 'uncommon',
      weight: 8,
      behavior: 'floater',
      style: '上浮型',
      length: [22, 48],
      difficulty: 49,
      basePrice: 58,
      periods: ['dawn', 'day'],
      description: '高耸的背鳍像一面小旗，爱在清凉急流中逆水游动。',
    },
    {
      name: '赤眼鳟',
      kind: 'river_red_eye',
      location: 'river',
      rarity: 'uncommon',
      weight: 7,
      behavior: 'mixed',
      style: '混合型',
      length: [20, 55],
      difficulty: 52,
      basePrice: 61,
      periods: ['day', 'dusk'],
      description: '红色眼圈很醒目，常在河湾的缓流边寻找食物。',
    },
    {
      name: '山女鳟',
      kind: 'river_yamame',
      location: 'river',
      rarity: 'rare',
      weight: 5,
      behavior: 'dart',
      style: '乱窜型',
      length: [20, 45],
      difficulty: 64,
      basePrice: 86,
      periods: ['dawn'],
      description: '带着椭圆斑纹的溪流鳟鱼，清晨在浅滩边迅速掠过。',
    },
    {
      name: '江鳕',
      kind: 'river_burbot',
      location: 'river',
      rarity: 'rare',
      weight: 4,
      behavior: 'sinker',
      style: '下沉型',
      length: [35, 85],
      difficulty: 65,
      basePrice: 92,
      periods: ['night'],
      weathers: ['cloudy', 'rain'],
      description: '有一根下颌须的河底猎手，偏爱昏暗寒冷的流水。',
    },
    {
      name: '鲥鱼',
      kind: 'river_hilsa',
      location: 'river',
      rarity: 'epic',
      weight: 2,
      behavior: 'mixed',
      style: '混合型',
      length: [35, 70],
      difficulty: 78,
      basePrice: 165,
      periods: ['dusk', 'night'],
      weathers: ['rain'],
      description: '银鳞洄游鱼，雨后水流上涨时偶尔进入这段河道。',
    },
    {
      name: '沙丁鱼',
      kind: 'coast_sardine',
      location: 'coast',
      rarity: 'common',
      weight: 23,
      behavior: 'smooth',
      style: '平稳型',
      length: [10, 25],
      difficulty: 16,
      basePrice: 23,
    },
    {
      name: '鳀鱼',
      kind: 'coast_anchovy',
      location: 'coast',
      rarity: 'common',
      weight: 19,
      behavior: 'dart',
      style: '乱窜型',
      length: [8, 20],
      difficulty: 32,
      basePrice: 27,
    },
    {
      name: '鲱鱼',
      kind: 'coast_herring',
      location: 'coast',
      rarity: 'common',
      weight: 17,
      behavior: 'floater',
      style: '上浮型',
      length: [18, 36],
      difficulty: 30,
      basePrice: 29,
    },
    {
      name: '竹荚鱼',
      kind: 'coast_horsemackerel',
      location: 'coast',
      rarity: 'common',
      weight: 15,
      behavior: 'mixed',
      style: '混合型',
      length: [16, 40],
      difficulty: 38,
      basePrice: 34,
    },
    {
      name: '鲭鱼',
      kind: 'coast_mackerel',
      location: 'coast',
      rarity: 'uncommon',
      weight: 12,
      behavior: 'dart',
      style: '乱窜型',
      length: [22, 55],
      difficulty: 55,
      basePrice: 53,
    },
    {
      name: '海鲈',
      kind: 'coast_seabass',
      location: 'coast',
      rarity: 'uncommon',
      weight: 11,
      behavior: 'mixed',
      style: '混合型',
      length: [30, 80],
      difficulty: 52,
      basePrice: 58,
    },
    {
      name: '鲽鱼',
      kind: 'coast_flounder',
      location: 'coast',
      rarity: 'uncommon',
      weight: 10,
      behavior: 'sinker',
      style: '下沉型',
      length: [25, 65],
      difficulty: 49,
      basePrice: 56,
    },
    {
      name: '真鲷',
      kind: 'coast_seabream',
      location: 'coast',
      rarity: 'rare',
      weight: 7,
      behavior: 'floater',
      style: '上浮型',
      length: [30, 75],
      difficulty: 64,
      basePrice: 82,
    },
    {
      name: '石斑鱼',
      kind: 'coast_grouper',
      location: 'coast',
      rarity: 'rare',
      weight: 6,
      behavior: 'sinker',
      style: '下沉型',
      length: [35, 100],
      difficulty: 70,
      basePrice: 95,
    },
    {
      name: '黄尾鰤',
      kind: 'coast_yellowtail',
      location: 'coast',
      rarity: 'rare',
      weight: 5,
      behavior: 'dart',
      style: '乱窜型',
      length: [50, 125],
      difficulty: 75,
      basePrice: 105,
    },
    {
      name: '蓝鳍金枪鱼',
      kind: 'coast_tuna',
      location: 'coast',
      rarity: 'epic',
      weight: 3,
      behavior: 'dart',
      style: '乱窜型',
      length: [90, 180],
      difficulty: 86,
      basePrice: 170,
    },
    {
      name: '剑鱼',
      kind: 'coast_swordfish',
      location: 'coast',
      rarity: 'legendary',
      weight: 1.5,
      behavior: 'mixed',
      style: '混合型',
      length: [110, 200],
      difficulty: 98,
      basePrice: 350,
    },
    {
      name: '银鲳',
      kind: 'coast_pomfret',
      location: 'coast',
      rarity: 'common',
      weight: 11,
      behavior: 'smooth',
      style: '平稳型',
      length: [18, 40],
      difficulty: 26,
      basePrice: 33,
      periods: ['day', 'dusk'],
      description: '银亮的扁平鱼，常随近岸潮流成群出现。',
    },
    {
      name: '飞鱼',
      kind: 'coast_flyingfish',
      location: 'coast',
      rarity: 'uncommon',
      weight: 7,
      behavior: 'floater',
      style: '上浮型',
      length: [18, 36],
      difficulty: 53,
      basePrice: 65,
      periods: ['dawn', 'day'],
      weathers: ['sunny'],
      description: '宽大的胸鳍能托着它短暂滑翔，海面明亮时最容易发现。',
    },
    {
      name: '带鱼',
      kind: 'coast_cutlassfish',
      location: 'coast',
      rarity: 'uncommon',
      weight: 8,
      behavior: 'sinker',
      style: '下沉型',
      length: [50, 120],
      difficulty: 55,
      basePrice: 70,
      periods: ['dusk', 'night'],
      description: '银白色的长带状身体，在昏暗海水里映出冷光。',
    },
    {
      name: '海鳗',
      kind: 'coast_conger',
      location: 'coast',
      rarity: 'rare',
      weight: 5,
      behavior: 'sinker',
      style: '下沉型',
      length: [60, 135],
      difficulty: 68,
      basePrice: 98,
      periods: ['night'],
      weathers: ['cloudy', 'rain'],
      description: '白天藏在礁石缝里，阴雨夜晚才沿海底出来觅食。',
    },
    {
      name: '鲣鱼',
      kind: 'coast_skipjack',
      location: 'coast',
      rarity: 'rare',
      weight: 5,
      behavior: 'dart',
      style: '乱窜型',
      length: [45, 95],
      difficulty: 72,
      basePrice: 112,
      periods: ['day'],
      weathers: ['sunny', 'cloudy'],
      description: '游速极快的成群猎手，银腹上的深色条纹很有辨识度。',
    },
    {
      name: '鮟鱇',
      kind: 'coast_anglerfish',
      location: 'coast',
      rarity: 'epic',
      weight: 2,
      behavior: 'sinker',
      style: '下沉型',
      length: [35, 90],
      difficulty: 82,
      basePrice: 190,
      periods: ['night'],
      weathers: ['rain'],
      description: '头顶的发光诱饵在黑暗里晃动，暴雨夜才接近海岸。',
    },
    {
      name: '蓝鳃太阳鱼',
      kind: 'lake_bluegill',
      location: 'lake',
      rarity: 'common',
      weight: 9,
      behavior: 'mixed',
      style: '混合型',
      length: [12, 30],
      difficulty: 24,
      basePrice: 25,
      periods: ['day'],
      description: '扁圆的身体带着暗色横纹，鳃盖后缘有醒目的蓝黑斑，常在岸边浅水活动。',
    },
    {
      name: '南瓜籽太阳鱼',
      kind: 'lake_pumpkinseed',
      location: 'lake',
      rarity: 'common',
      weight: 8,
      behavior: 'smooth',
      style: '平稳型',
      length: [10, 28],
      difficulty: 22,
      basePrice: 25,
      description: '脸颊上的蓝绿纹路与橙色斑点像一幅小画，圆短的身形在水草间缓缓游动。',
    },
    {
      name: '欧洲红眼鱼',
      kind: 'lake_rudd',
      location: 'lake',
      rarity: 'common',
      weight: 8,
      behavior: 'floater',
      style: '上浮型',
      length: [12, 35],
      difficulty: 26,
      basePrice: 29,
      periods: ['dawn', 'dusk'],
      description: '金铜色鳞片和鲜红的腹鳍很显眼，上翘的小嘴适合在水面附近觅食。',
    },
    {
      name: '拟鲤',
      kind: 'lake_roach',
      location: 'lake',
      rarity: 'common',
      weight: 9,
      behavior: 'smooth',
      style: '平稳型',
      length: [12, 40],
      difficulty: 28,
      basePrice: 30,
      description: '银亮的鳞片下映着暖红色鱼鳍，外形修长，喜欢在湖湾里成群缓游。',
    },
    {
      name: '黄鲈',
      kind: 'lake_yellowperch',
      location: 'lake',
      rarity: 'uncommon',
      weight: 7,
      behavior: 'mixed',
      style: '混合型',
      length: [18, 45],
      difficulty: 46,
      basePrice: 50,
      weathers: ['cloudy', 'rain'],
      description: '金黄色体侧有多道黑色横带，背上两枚独立的背鳍让它格外好认。',
    },
    {
      name: '匙吻鲟',
      kind: 'lake_paddlefish',
      location: 'lake',
      rarity: 'legendary',
      weight: 0.9,
      behavior: 'smooth',
      style: '平稳型',
      length: [180, 360],
      difficulty: 98,
      basePrice: 340,
      periods: ['dawn', 'dusk'],
      weathers: ['cloudy', 'rain'],
      description:
        '现实中的匙吻鲟因栖息地变化等因素而减少，长而扁的匙状吻部格外醒目；游戏中它被放大为湖泊巨鱼。',
    },
    {
      name: '欧洲鱥',
      kind: 'river_minnow',
      location: 'river',
      rarity: 'common',
      weight: 9,
      behavior: 'mixed',
      style: '混合型',
      length: [6, 16],
      difficulty: 19,
      basePrice: 23,
      periods: ['dawn', 'day'],
      description: '小巧的纺锤形身体带着深色侧纹，常成群穿过清浅的河段。',
    },
    {
      name: '欧鲌',
      kind: 'river_bleak',
      location: 'river',
      rarity: 'common',
      weight: 9,
      behavior: 'floater',
      style: '上浮型',
      length: [10, 25],
      difficulty: 26,
      basePrice: 26,
      description: '薄薄的银色身躯像一片闪光，上翘的小嘴贴着水面寻找食物。',
    },
    {
      name: '石鳅',
      kind: 'river_stoneloach',
      location: 'river',
      rarity: 'common',
      weight: 8,
      behavior: 'sinker',
      style: '下沉型',
      length: [8, 20],
      difficulty: 22,
      basePrice: 24,
      periods: ['dusk', 'night'],
      description: '砂褐色斑纹能藏进河床，嘴边的短须帮助它在石缝间搜寻食物。',
    },
    {
      name: '小口黑鲈',
      kind: 'river_smallmouth',
      location: 'river',
      rarity: 'uncommon',
      weight: 7,
      behavior: 'mixed',
      style: '混合型',
      length: [25, 60],
      difficulty: 51,
      basePrice: 58,
      description: '铜褐色体侧带着深色竖纹，嘴角不过眼后缘，常守在石滩附近。',
    },
    {
      name: '欧鲦',
      kind: 'river_chub',
      location: 'river',
      rarity: 'uncommon',
      weight: 7,
      behavior: 'smooth',
      style: '平稳型',
      length: [25, 70],
      difficulty: 47,
      basePrice: 55,
      periods: ['day', 'dusk'],
      description: '宽阔的头部和大鳞片很有辨识度，常在河湾的急缓交界处徘徊。',
    },
    {
      name: '欧洲鲟',
      kind: 'river_europeansturgeon',
      location: 'river',
      rarity: 'legendary',
      weight: 0.9,
      behavior: 'mixed',
      style: '混合型',
      length: [200, 420],
      difficulty: 103,
      basePrice: 360,
      periods: ['dusk', 'night'],
      weathers: ['rain'],
      description:
        '现实中的欧洲鲟野生种群非常稀少，身上排列着硬质骨板；游戏中它沿雨夜上涨的河水洄游。',
    },
    {
      name: '鲻鱼',
      kind: 'coast_mullet',
      location: 'coast',
      rarity: 'common',
      weight: 9,
      behavior: 'smooth',
      style: '平稳型',
      length: [20, 65],
      difficulty: 29,
      basePrice: 34,
      description: '银灰色身体有细细的纵纹，小嘴与两枚分离的背鳍是它的特点。',
    },
    {
      name: '牙鳕',
      kind: 'coast_whiting',
      location: 'coast',
      rarity: 'common',
      weight: 8,
      behavior: 'sinker',
      style: '下沉型',
      length: [20, 55],
      difficulty: 31,
      basePrice: 35,
      periods: ['dusk', 'night'],
      description: '体侧泛着银白光泽，连续排列的三枚背鳍像一串小旗，偏爱昏暗海水。',
    },
    {
      name: '欧洲颌针鱼',
      kind: 'coast_garfish',
      location: 'coast',
      rarity: 'common',
      weight: 8,
      behavior: 'floater',
      style: '上浮型',
      length: [35, 90],
      difficulty: 36,
      basePrice: 38,
      periods: ['day'],
      description: '细长的上下颌像一把镊子，蓝绿色背部在水面附近一闪而过。',
    },
    {
      name: '黑鲷',
      kind: 'coast_blackseabream',
      location: 'coast',
      rarity: 'uncommon',
      weight: 7,
      behavior: 'mixed',
      style: '混合型',
      length: [25, 65],
      difficulty: 56,
      basePrice: 68,
      weathers: ['cloudy', 'rain'],
      description: '深灰色体侧带着不显眼的横带，厚实的嘴与高背鳍适合在礁石边活动。',
    },
    {
      name: '鲯鳅',
      kind: 'coast_mahimahi',
      location: 'coast',
      rarity: 'epic',
      weight: 2,
      behavior: 'dart',
      style: '乱窜型',
      length: [70, 170],
      difficulty: 84,
      basePrice: 180,
      periods: ['dawn', 'day'],
      weathers: ['sunny', 'cloudy'],
      description: '蓝绿背部与金黄色体侧像流动的宝石，高耸的额头后是一条延伸到尾部的长背鳍。',
    },
    {
      name: '苏眉',
      kind: 'coast_humpheadwrasse',
      location: 'coast',
      rarity: 'legendary',
      weight: 0.9,
      behavior: 'floater',
      style: '上浮型',
      length: [160, 340],
      difficulty: 101,
      basePrice: 360,
      periods: ['dawn', 'day'],
      weathers: ['cloudy'],
      description:
        '现实中的曲纹唇鱼又称苏眉，是受保护的稀少鱼类；厚唇、隆起的额头与细密蓝绿纹路让它像一位海中长者。',
    },
    {
      name: '攀鲈',
      kind: 'jungle_climbingperch',
      location: 'jungle',
      rarity: 'common',
      weight: 10,
      behavior: 'smooth',
      style: '平稳型',
      length: [10, 26],
      difficulty: 15,
      basePrice: 24,
      description: '厚实的橄榄色身体配着硬棘背鳍，常在树根旁的缓水里活动。',
    },
    {
      name: '三星丝足鲈',
      kind: 'jungle_threespotgourami',
      location: 'jungle',
      rarity: 'common',
      weight: 9,
      behavior: 'smooth',
      style: '平稳型',
      length: [9, 22],
      difficulty: 20,
      basePrice: 26,
      description: '银蓝色身体有两枚暗斑，细长腹鳍像触须，喜欢水草丰茂的浅湾。',
    },
    {
      name: '蛇皮丝足鲈',
      kind: 'jungle_snakeskingourami',
      location: 'jungle',
      rarity: 'common',
      weight: 8,
      behavior: 'floater',
      style: '上浮型',
      length: [12, 30],
      difficulty: 25,
      basePrice: 28,
      description: '斜向暗纹像蛇皮，细长腹鳍轻探水面下的植物。',
    },
    {
      name: '鸣声丝足鲈',
      kind: 'jungle_croakinggourami',
      location: 'jungle',
      rarity: 'common',
      weight: 8,
      behavior: 'smooth',
      style: '平稳型',
      length: [4, 11],
      difficulty: 18,
      basePrice: 22,
      description: '小巧的褐色身躯带着横纹，安静时会藏在落叶与水草之间。',
    },
    {
      name: '月光丝足鲈',
      kind: 'jungle_moonlightgourami',
      location: 'jungle',
      rarity: 'common',
      weight: 7,
      behavior: 'floater',
      style: '上浮型',
      length: [10, 24],
      difficulty: 23,
      basePrice: 27,
      description: '细密的银白鳞片柔和反光，橙红色的眼睛很醒目。',
      periods: ['dusk', 'night'],
    },
    {
      name: '红尾波鱼',
      kind: 'jungle_redtailrasbora',
      location: 'jungle',
      rarity: 'common',
      weight: 7,
      behavior: 'dart',
      style: '乱窜型',
      length: [7, 21],
      difficulty: 30,
      basePrice: 29,
      description: '修长身体上的黑色横带十分清晰，红尾一摆便钻入浅水阴影。',
      periods: ['dawn', 'day'],
    },
    {
      name: '银色飞鲃',
      kind: 'jungle_silverflyingbarb',
      location: 'jungle',
      rarity: 'common',
      weight: 6,
      behavior: 'mixed',
      style: '混合型',
      length: [5, 13],
      difficulty: 27,
      basePrice: 25,
      description: '亮银色身体有一道深色横线，长须帮助它在缓水中寻找小食物。',
      weathers: ['cloudy', 'rain'],
    },
    {
      name: '爪哇鲃',
      kind: 'jungle_silverbarb',
      location: 'jungle',
      rarity: 'common',
      weight: 6,
      behavior: 'sinker',
      style: '下沉型',
      length: [15, 46],
      difficulty: 33,
      basePrice: 34,
      description: '宽阔的银色鳞片与暖黄鱼鳍相衬，常沿着浅湾缓缓觅食。',
    },
    {
      name: '铜色弓背鱼',
      kind: 'jungle_bronzefeatherback',
      location: 'jungle',
      rarity: 'uncommon',
      weight: 7,
      behavior: 'smooth',
      style: '平稳型',
      length: [20, 62],
      difficulty: 36,
      basePrice: 48,
      description: '弓起的背部呈铜灰色，绵长臀鳍像一条轻摆的丝带。',
    },
    {
      name: '长须鲿',
      kind: 'jungle_yellowcatfish',
      location: 'jungle',
      rarity: 'uncommon',
      weight: 6,
      behavior: 'sinker',
      style: '下沉型',
      length: [24, 78],
      difficulty: 41,
      basePrice: 54,
      description: '扁平头部伸出长须，青褐色身体贴着河底与浸水树根游动。',
    },
    {
      name: '大头胡鲶',
      kind: 'jungle_bigheadcatfish',
      location: 'jungle',
      rarity: 'uncommon',
      weight: 6,
      behavior: 'sinker',
      style: '下沉型',
      length: [22, 65],
      difficulty: 43,
      basePrice: 52,
      description: '宽头、长须与细密暗斑让它很容易辨认，偏爱泥底与缓流。',
      periods: ['dusk', 'night'],
    },
    {
      name: '条纹鳢',
      kind: 'jungle_stripedsnakehead',
      location: 'jungle',
      rarity: 'uncommon',
      weight: 5,
      behavior: 'mixed',
      style: '混合型',
      length: [28, 85],
      difficulty: 48,
      basePrice: 62,
      description: '长身躯带着斜纹，蛇形头部常从丛林浅湾的水草间探出。',
    },
    {
      name: '暹罗刺鳅',
      kind: 'jungle_peacockeel',
      location: 'jungle',
      rarity: 'uncommon',
      weight: 5,
      behavior: 'sinker',
      style: '下沉型',
      length: [20, 48],
      difficulty: 45,
      basePrice: 57,
      description: '尖细吻部在泥沙里探寻食物，背部后段排列着眼状斑。',
      periods: ['dusk', 'night'],
    },
    {
      name: '无须圆唇鲃',
      kind: 'jungle_beardlessbarb',
      location: 'jungle',
      rarity: 'uncommon',
      weight: 4,
      behavior: 'mixed',
      style: '混合型',
      length: [16, 36],
      difficulty: 40,
      basePrice: 50,
      description: '银色鳞片间有点状暗纹，尾根黑斑在水中格外显眼。',
    },
    {
      name: '眼斑弓背鱼',
      kind: 'jungle_clownfeatherback',
      location: 'jungle',
      rarity: 'rare',
      weight: 5,
      behavior: 'sinker',
      style: '下沉型',
      length: [45, 120],
      difficulty: 64,
      basePrice: 105,
      description: '银灰色弓背下排列着眼状黑斑，黄昏后在深潭边巡游。',
      periods: ['dusk', 'night'],
    },
    {
      name: '小鳞射水鱼',
      kind: 'jungle_smallscalearcherfish',
      location: 'jungle',
      rarity: 'rare',
      weight: 4,
      behavior: 'floater',
      style: '上浮型',
      length: [12, 32],
      difficulty: 55,
      basePrice: 90,
      description: '银黄身体带着深色条斑，上翘的嘴让它善于在水面附近捕食。',
      periods: ['dawn', 'day'],
    },
    {
      name: '巨型丝足鲈',
      kind: 'jungle_giantgourami',
      location: 'jungle',
      rarity: 'rare',
      weight: 4,
      behavior: 'smooth',
      style: '平稳型',
      length: [35, 92],
      difficulty: 59,
      basePrice: 98,
      description: '宽厚身躯披着淡铜色鳞片，慢而有力地穿过浸水树根。',
    },
    {
      name: '红鳍锡箔鲃',
      kind: 'jungle_tinfoilbarb',
      location: 'jungle',
      rarity: 'rare',
      weight: 3,
      behavior: 'mixed',
      style: '混合型',
      length: [22, 52],
      difficulty: 62,
      basePrice: 100,
      description: '大片银鳞仿佛锡箔，红色鱼鳍与黑色尾缘构成醒目的标记。',
    },
    {
      name: '大鳞鲃',
      kind: 'jungle_hampalabarb',
      location: 'jungle',
      rarity: 'rare',
      weight: 3,
      behavior: 'dart',
      style: '乱窜型',
      length: [35, 90],
      difficulty: 72,
      basePrice: 120,
      description: '体侧深色竖带像一道腰带，红尾有黑边，冲刺时充满力量。',
      weathers: ['cloudy', 'rain'],
    },
    {
      name: '亚洲龙鱼',
      kind: 'jungle_asianarowana',
      location: 'jungle',
      rarity: 'epic',
      weight: 3,
      behavior: 'floater',
      style: '上浮型',
      length: [55, 140],
      difficulty: 76,
      basePrice: 175,
      description: '大片铜金色鳞片覆着修长身体，两根下颌须随水流轻摆。',
      periods: ['dawn', 'dusk'],
    },
    {
      name: '小盾鳢',
      kind: 'jungle_giantsnakehead',
      location: 'jungle',
      rarity: 'epic',
      weight: 2.5,
      behavior: 'dart',
      style: '乱窜型',
      length: [70, 170],
      difficulty: 86,
      basePrice: 195,
      description: '宽大的蛇形头部与青黑斑纹显出掠食者的气势，突进后常停下蓄力。',
      weathers: ['cloudy', 'rain'],
    },
    {
      name: '低眼巨鲶',
      kind: 'jungle_stripedpangasius',
      location: 'jungle',
      rarity: 'epic',
      weight: 2.5,
      behavior: 'mixed',
      style: '混合型',
      length: [80, 180],
      difficulty: 81,
      basePrice: 185,
      description: '低位的眼睛与银灰色无鳞身躯很有辨识度，宽阔深潭里能见到它的身影。',
    },
    {
      name: '湄公河巨型鲶',
      kind: 'jungle_mekonggiantcatfish',
      location: 'jungle',
      rarity: 'legendary',
      weight: 1.1,
      behavior: 'smooth',
      style: '平稳型',
      length: [240, 520],
      difficulty: 98,
      basePrice: 380,
      description:
        '青灰色巨躯像河湾中缓缓移动的阴影。原型是现实中稀少的大型淡水鱼，游戏体长有所放大。',
    },
    {
      name: '暹罗巨鲤',
      kind: 'jungle_siamesegiantcarp',
      location: 'jungle',
      rarity: 'legendary',
      weight: 0.9,
      behavior: 'mixed',
      style: '混合型',
      length: [220, 480],
      difficulty: 103,
      basePrice: 390,
      description:
        '硕大的头部与银黑色大鳞片透出古老气息。原型是现实中稀少的巨鲤，游戏体长有所放大。',
      periods: ['dusk', 'night'],
      weathers: ['cloudy', 'rain'],
    },
  ]);
  const PERIODS = Object.freeze({ dawn: '清晨', day: '白天', dusk: '黄昏', night: '夜晚' });
  const WEATHERS = Object.freeze({ sunny: '晴', cloudy: '阴', rain: '雨' });
  const WEATHER_CYCLE = Object.freeze(['sunny', 'cloudy', 'rain', 'sunny', 'sunny', 'rain']);
  const FISH_DESCRIPTIONS = Object.freeze({
    gold: '常见的湖鱼，游动温和，是许多钓手的第一条收获。',
    lake_carp: '身形厚实，受惊时会突然往水底深处钻去。',
    lake_yellowcat: '嘴边有长须，常贴着湖底寻找食物。',
    sky: '体形宽扁，喜欢在中上层水域慢慢巡游。',
    lake_grasscarp: '食量很大的草食鱼，拉力比看起来更沉。',
    mud: '夜色里的湖底猎手，嘴边的触须能感知动静。',
    silver: '伏在水草边等待猎物，突然发力时很难预判。',
    violet: '斑纹鲜明的伏击者，追逐小鱼时动作很急。',
    lake_snakehead: '能够在浅水中潜伏很久，一上钩就拼命往草里钻。',
    lake_silvercarp: '宽大的身体在水中缓缓上浮，甩头时力量惊人。',
    lake_bighead: '巨大的头部很好辨认，是湖中难得的大鱼。',
    star: '深水里的黑影，只有经验丰富的钓手才敢与它周旋。',
    teal: '溪流中的小型快鱼，常在明亮的浅滩结伴游动。',
    river_zacco: '鳍色醒目，在石缝间乱窜得非常灵活。',
    river_loach: '喜欢贴着河底钻进砂砾，身上有细小的斑纹。',
    river_gudgeon: '安静的小型底栖鱼，常躲在缓流的石块旁。',
    river_ayu: '银亮的身体顺着急流穿梭，动作轻快。',
    river_perch: '背鳍带刺，在河湾和水草边巡游。',
    red: '身侧有彩色光带，受惊时会迅速折返。',
    river_browntrout: '褐色斑点铺满身体，善于借水流突然加速。',
    river_pike: '细长的伏击者，突然冲刺时几乎不给人反应。',
    frost: '弯曲的长身藏在河底阴影里，夜间更显活跃。',
    river_salmon: '强壮的洄游鱼，逆流而上时也能保持速度。',
    abyss: '河道深处的庞大掠食者，冲刺如同一道暗影。',
    coast_sardine: '成群聚集的银色小鱼，海面上常见闪亮鱼群。',
    coast_anchovy: '小而敏捷，鱼群转向时会一同闪光。',
    coast_herring: '喜欢集群行动，常在海岸附近的中层水域游动。',
    coast_horsemackerel: '尾部有力，能沿着海流稳定前进。',
    coast_mackerel: '背上有波浪纹，冲刺时几乎像一支箭。',
    coast_seabass: '游走于礁石边缘，拉扯节奏忽快忽慢。',
    coast_flounder: '扁平的身体伏在海底，颜色与沙地很接近。',
    coast_seabream: '鳞片带着暖色光泽，常在礁石附近觅食。',
    coast_grouper: '体型厚实的礁区鱼，惯于往岩缝里钻。',
    coast_yellowtail: '黄色尾鳍一闪而过，游速很快。',
    coast_tuna: '远游的大型鱼，力量和速度都让人吃惊。',
    coast_swordfish: '细长的剑状吻部是它的标志，海上罕见的强敌。',
  });
  const SKILLS = Object.freeze({
    steady: { name: '稳竿手', description: '捕捉条额外增加 8 像素。' },
    tracker: { name: '寻鱼人', description: '稀有及以上鱼出现权重 +20%，钓获经验 +8%。' },
    calm: { name: '沉着收线', description: '鱼脱离捕捉条时，进度流失降低 10%。' },
    control: { name: '控竿专家', description: '捕捉条的上浮和下落加速度提高 8%。' },
    legendHunter: { name: '传奇猎手', description: '史诗与传说鱼出现权重 +25%。' },
    brawler: { name: '搏鱼专家', description: '追踪稀有及以上鱼时，命中进度增加 8%。' },
    patience: { name: '耐心收线', description: '鱼脱离捕捉条时，进度流失再降低 6%。' },
    reader: { name: '识流者', description: '鱼改变移动目标的节奏放慢 8%。' },
    master: { name: '老练竿法', description: '捕捉条额外增加 8 像素。' },
    deepSeeker: {
      name: '深水行者',
      description: '史诗与传说鱼出现权重 +20%，稀有及以上鱼的命中进度 +5%。',
    },
  });
  const GEAR = Object.freeze({
    trainingRod: {
      name: '训练竿',
      slot: 'rod',
      cost: 25,
      tackleSlots: 0,
      baitAllowed: false,
      description: '不足 136 像素时提升捕捉条；脱离流失降为 2/3。只能钓难度低于 50 的普通品质鱼。',
    },
    bambooPole: {
      name: '竹竿',
      slot: 'rod',
      cost: 500,
      tackleSlots: 0,
      baitAllowed: false,
      description: '基础鱼竿，不使用鱼饵或渔具。',
    },
    fiberglassRod: {
      name: '玻璃纤维竿',
      slot: 'rod',
      cost: 1800,
      tackleSlots: 0,
      baitAllowed: true,
      description: '可以使用鱼饵。',
    },
    iridiumRod: {
      name: '铱金鱼竿',
      slot: 'rod',
      cost: 7500,
      tackleSlots: 1,
      baitAllowed: true,
      description: '可以使用鱼饵和 1 件渔具。',
    },
    advancedRod: {
      name: '进阶铱金鱼竿',
      slot: 'rod',
      cost: 25000,
      tackleSlots: 2,
      baitAllowed: true,
      description: '可以使用鱼饵和 2 件渔具，允许相同渔具叠加。',
    },
    legendRod: {
      name: '传说钓手竿',
      slot: 'rod',
      cost: 0,
      tackleSlots: 3,
      baitAllowed: true,
      legendaryRequired: 10,
      description:
        '累计钓获 10 条传说鱼后免费领取。可使用鱼饵和 3 件渔具，同一种最多装两件；鱼竿本身不增加条长或容错。',
    },
    corkBobber: {
      name: '软木浮漂',
      slot: 'tackle',
      cost: 750,
      description: '捕捉条高度 +24 像素。',
      effects: { barHeight: 24 / 568 },
    },
    leadBobber: {
      name: '铅制浮漂',
      slot: 'tackle',
      cost: 200,
      description: '触底反弹速度变为原来的 1/10。',
      effects: { bottomBounce: 0.1 },
    },
    trapBobber: {
      name: '陷阱浮漂',
      slot: 'tackle',
      cost: 500,
      description: '脱离捕捉条时，进度流失降低 1/3；两件叠加降低 50%。',
    },
    barbedHook: {
      name: '倒刺鱼钩',
      slot: 'tackle',
      cost: 1000,
      description: '鱼在捕捉条内时，捕捉条会轻微追随鱼；乱窜鱼仍需手动控制。',
    },
    qualityBobber: {
      name: '品质浮漂',
      slot: 'tackle',
      cost: 0,
      unlock: 'perfect',
      description: '鱼的品质提升一级；累计 3 次完美捕获可领第一件，10 次可领第二件。',
    },
    curiosityLure: {
      name: '好奇心鱼钩',
      slot: 'tackle',
      cost: 0,
      unlock: 'legendary',
      description: '传说鱼的出现权重 ×2；钓获首条传说鱼后可领取。',
    },
    spinner: {
      name: '旋转式亮片',
      slot: 'tackle',
      cost: 500,
      description: '上钩等待时间的上限减少 3.75 游戏秒。',
    },
    dressedSpinner: {
      name: '精装旋转亮片',
      slot: 'tackle',
      cost: 1000,
      description: '上钩等待时间的上限减少 7.5 游戏秒。',
    },
    sonarBobber: {
      name: '声呐浮漂',
      slot: 'tackle',
      cost: 500,
      description: '上钩后显示鱼的种类。',
    },
  });
  const TACKLE_SLOTS = Object.freeze(['tackle1', 'tackle2', 'tackle3']);
  const BAITS = Object.freeze({
    basic: {
      name: '普通鱼饵',
      cost: 5,
      description: '下一竿等待上钩时间减半。',
      effects: { biteDelay: 0.5 },
    },
    glimmer: {
      name: '闪鳞鱼饵',
      cost: 45,
      description: '下一竿稀有及以上鱼出现权重 +45%，等待时间减半。',
      effects: { rareWeight: 1.45, biteDelay: 0.5 },
    },
    abyss: {
      name: '深水鱼饵',
      cost: 95,
      description: '下一竿史诗与传说鱼出现权重 ×2，等待时间减半；鱼速度 +4%、脱钩流失 +8%。',
      effects: { epicWeight: 2, fishSpeed: 1.04, progressLoss: 1.08, biteDelay: 0.5 },
    },
    deluxe: {
      name: '豪华鱼饵',
      cost: 100,
      description: '等待上钩时间减少 67%，捕捉条 +12 像素。',
      effects: { biteDelay: 0.33, barHeight: 12 / 568 },
    },
  });
  const BASKET_CAPACITY = 80;
  const DEBRIS = Object.freeze({
    driftwood: { name: '漂流木', icon: '🪵', value: 2 },
    bottle: { name: '碎玻璃瓶', icon: '🍾', value: 1 },
    boot: { name: '旧靴子', icon: '🥾', value: 1 },
    line: { name: '缠绕的废鱼线', icon: '🧵', value: 3 },
  });
  const CONTRACT_SLOTS = Object.freeze({
    delivery1: { type: 'delivery', target: 3 },
    delivery2: { type: 'delivery', target: 2 },
    catch: { type: 'catch', target: 4 },
    perfect: { type: 'perfect', target: 2 },
    cleanup: { type: 'cleanup', target: 4 },
  });
  const QUALITY_ORDER = Object.freeze({ 普通: 0, 银星: 1, 金星: 2, 铱星: 3 });
  const QUALITY_NAMES = Object.freeze(['普通', '银星', '金星', '铱星']);
  const $ = (id) => localized(root.querySelector('#' + id));
  const ui = Object.freeze({
    scenery: $('scenery'),
    fishingWater: $('fishingWater'),
    rodBody: $('rodBody'),
    rodTexture: $('rodTexture'),
    fishingLine: $('fishingLine'),
    bobber: $('bobber'),
    sceneTime: $('sceneTime'),
    sceneMessage: $('sceneMessage'),
    fishName: $('fishName'),
    phaseBadge: $('phaseBadge'),
    track: $('track'),
    catchBar: $('catchBar'),
    fish: $('fish'),
    progressTrack: $('progressTrack'),
    progressFill: $('progressFill'),
    progressValue: $('progressValue'),
    hintLine: $('hintLine'),
    controls: $('controls'),
    startButton: $('startButton'),
    holdButton: $('holdButton'),
    rarityValue: $('rarityValue'),
    caughtCount: $('caughtCount'),
    streakCount: $('streakCount'),
    coinCount: $('coinCount'),
    overlay: $('overlay'),
    overlayIcon: $('overlayIcon'),
    catchArt: $('catchArt'),
    overlayTitle: $('overlayTitle'),
    overlayText: $('overlayText'),
    catchBadges: $('catchBadges'),
    catchGrowth: $('catchGrowth'),
    catchXp: $('catchXp'),
    catchLevel: $('catchLevel'),
    catchXpTrack: $('catchXpTrack'),
    catchXpFill: $('catchXpFill'),
    catchCoins: $('catchCoins'),
    catchUnlocks: $('catchUnlocks'),
    settingsButton: $('settingsButton'),
    settingsModal: $('settingsModal'),
    settingsClose: $('settingsClose'),
    soundEnabled: $('soundEnabled'),
    soundVolume: $('soundVolume'),
    soundVolumeValue: $('soundVolumeValue'),
    reduceFeedback: $('reduceFeedback'),
    overlayButton: $('overlayButton'),
    overlaySecondary: $('overlaySecondary'),
    overlayBasket: $('overlayBasket'),
    levelLabel: $('levelLabel'),
    xpLabel: $('xpLabel'),
    xpTrack: $('xpTrack'),
    xpFill: $('xpFill'),
    skillButton: $('skillButton'),
    skillModal: $('skillModal'),
    skillClose: $('skillClose'),
    skillIntro: $('skillIntro'),
    skillChoices: $('skillChoices'),
    skillSummary: $('skillSummary'),
    shopButton: $('shopButton'),
    shopModal: $('shopModal'),
    shopClose: $('shopClose'),
    shopIntro: $('shopIntro'),
    shopItems: $('shopItems'),
    shopSummary: $('shopSummary'),
    loadoutLabel: $('loadoutLabel'),
    basketButton: $('basketButton'),
    basketModal: $('basketModal'),
    basketClose: $('basketClose'),
    basketIntro: $('basketIntro'),
    basketTotal: $('basketTotal'),
    basketItems: $('basketItems'),
    sellAllButton: $('sellAllButton'),
    catalogButton: $('catalogButton'),
    catalogModal: $('catalogModal'),
    catalogClose: $('catalogClose'),
    catalogIntro: $('catalogIntro'),
    catalogTabs: $('catalogTabs'),
    catalogItems: $('catalogItems'),
    locationButton: $('locationButton'),
    restButton: $('restButton'),
    locationModal: $('locationModal'),
    locationClose: $('locationClose'),
    locationItems: $('locationItems'),
    contractsButton: $('contractsButton'),
    contractsModal: $('contractsModal'),
    contractsClose: $('contractsClose'),
    contractsIntro: $('contractsIntro'),
    contractsNotice: $('contractsNotice'),
    contractsItems: $('contractsItems'),
    respecButton: $('respecButton'),
    respecNotice: $('respecNotice'),
    debrisSummary: $('debrisSummary'),
    recycleButton: $('recycleButton'),
    treasureMarker: $('treasureMarker'),
    treasureStatus: $('treasureStatus'),
    treasureLabel: $('treasureLabel'),
    treasureFill: $('treasureFill'),
    treasureValue: $('treasureValue'),
    treasureReward: $('treasureReward'),
    weatherRain: $('weatherRain'),
    basketBadge: $('basketBadge'),
    contractBadge: $('contractBadge'),
  });
  const clamp = (value, min, max) => Math.min(max, Math.max(min, value));
  function normalizeLoadout(loadout, ownedGear = null) {
    const rod =
      Object.hasOwn(GEAR, loadout?.rod) && GEAR[loadout.rod].slot === 'rod'
        ? loadout.rod
        : 'bambooPole';
    const result = { rod, tackle1: null, tackle2: null, tackle3: null };
    const used = new Map();
    for (const slot of TACKLE_SLOTS.slice(0, GEAR[rod].tackleSlots)) {
      const id = loadout?.[slot];
      if (!Object.hasOwn(GEAR, id) || GEAR[id].slot !== 'tackle') continue;
      const available = ownedGear
        ? Math.min(2, ownedGear.filter((entry) => entry === id).length)
        : 2;
      const copies = used.get(id) || 0;
      if (copies >= available) continue;
      result[slot] = id;
      used.set(id, copies + 1);
    }
    return result;
  }
  const fishArtSrc = (fish) => `/assets/lake-notes/fish-${fish.kind}.webp`;
  const itemArtSrc = (id) => `/assets/lake-notes/item-${id}.webp`;
  const rodArtSrc = (id) => `/assets/lake-notes/rod-${id}.webp`;
  // Measured tip-ring and handle-butt anchors in each compressed texture.
  const ROD_SPRITES = Object.freeze({
    trainingRod: { width: 512, height: 768, tip: [255.5, 18], butt: [261.5, 750.5] },
    bambooPole: { width: 512, height: 768, tip: [254, 15.5], butt: [256, 752.5] },
    fiberglassRod: { width: 512, height: 768, tip: [256.5, 15], butt: [259.5, 751.5] },
    iridiumRod: { width: 512, height: 768, tip: [259.5, 10], butt: [264, 757] },
    advancedRod: { width: 512, height: 768, tip: [252.5, 14.5], butt: [258, 753] },
    legendRod: { width: 512, height: 768, tip: [253.75, 16], butt: [258, 756] },
  });
  // Cosmetic palettes do not alter tackle effects. Two different floats use both colors.
  const FLOAT_COLORS = Object.freeze({
    corkBobber: { name: '琥珀黄', color: '#e6b44b' },
    leadBobber: { name: '石板灰', color: '#89959e' },
    trapBobber: { name: '苔叶绿', color: '#71a674' },
    qualityBobber: { name: '紫水晶', color: '#b48bd3' },
    sonarBobber: { name: '湖水蓝', color: '#57c3d3' },
  });
  const periodAt = (minutes) =>
    minutes < 300 || minutes >= 1260
      ? 'night'
      : minutes < 540
        ? 'dawn'
        : minutes < 1020
          ? 'day'
          : 'dusk';
  const weatherForDay = (day) => WEATHER_CYCLE[(day - 1) % WEATHER_CYCLE.length];
  const fishConditionText = (fish) =>
    `${LOCATIONS[fish.location].name} · ${fish.periods ? fish.periods.map((id) => PERIODS[id]).join('、') : '全天'} · ${fish.weathers ? fish.weathers.map((id) => WEATHERS[id]).join('、') : '任何天气'}`;
  const STARDEW_XP = Object.freeze([0, 100, 380, 770, 1300, 2150, 3300, 4800, 6900, 10000, 15000]);
  const xpForLevel = (level) =>
    level % 2 === 0
      ? STARDEW_XP[level / 2]
      : Math.floor((STARDEW_XP[Math.floor(level / 2)] + STARDEW_XP[Math.ceil(level / 2)]) / 2);
  function levelFromXp(xp) {
    let level = 0;
    while (level < CONFIG.maxLevel && xp >= xpForLevel(level + 1)) level++;
    return level;
  }
  function contractReward(contract) {
    if (contract.type === 'delivery')
      return {
        coins: Math.ceil(
          FISH_TYPES.find((fish) => fish.kind === contract.kind).basePrice * contract.target * 1.8,
        ),
        bait: 'basic',
        count: 3,
      };
    if (contract.type === 'perfect') return { coins: 220, bait: 'glimmer', count: 1 };
    if (contract.type === 'cleanup') return { coins: 140, bait: 'basic', count: 5 };
    return { coins: 150, bait: 'basic', count: 3 };
  }
  class InputController {
    constructor(game) {
      this.game = game;
      this.spaceHeld = false;
      this.pointers = new Set();
      // Cover nearby labels and dynamically created menus as well as the hold button.
      const root = $('gameRoot');
      for (const type of ['contextmenu', 'selectstart', 'dragstart']) {
        root.addEventListener(type, (event) => event.preventDefault(), { capture: true });
      }
      const clearSelection = () => {
        const selection = document.getSelection?.();
        if (selection?.isCollapsed === false) selection.removeAllRanges();
      };
      on(document, 'selectionchange', () => {
        if (this.game.state === 'playing' && this.held) clearSelection();
      });
      on(document, 'keydown', (event) => {
        if (event.code !== 'Space' || this.game.state !== 'playing' || this.game.paused) return;
        event.preventDefault();
        if (!event.repeat) this.spaceHeld = true;
      });
      on(document, 'keyup', (event) => {
        if (event.code !== 'Space') return;
        if (this.game.state === 'playing') event.preventDefault();
        this.spaceHeld = false;
      });
      for (const target of [ui.track, ui.holdButton, ui.scenery]) {
        target.addEventListener('selectstart', (event) => event.preventDefault());
        target.addEventListener('dragstart', (event) => event.preventDefault());
        target.addEventListener('pointerdown', (event) => {
          if (this.game.state !== 'playing' || this.game.paused) return;
          if (
            target === ui.scenery &&
            event.target?.closest(
              'button, .track, .overlay, .skill-modal, .dock, .hud, .journal-tabs',
            )
          )
            return;
          event.preventDefault();
          clearSelection();
          this.pointers.add(event.pointerId);
          try {
            target.setPointerCapture(event.pointerId);
          } catch {
            /* pointer may already be gone */
          }
        });
      }
      const release = (event) => this.pointers.delete(event.pointerId);
      on(window, 'pointerup', release);
      on(window, 'pointercancel', release);
      ui.holdButton.addEventListener('contextmenu', (event) => event.preventDefault());
      ui.track.addEventListener('contextmenu', (event) => event.preventDefault());
    }
    get held() {
      return this.spaceHeld || this.pointers.size > 0;
    }
    clear() {
      this.spaceHeld = false;
      this.pointers.clear();
    }
  }

  const PREFERENCES_KEY = 'lake-notes-preferences-v1';
  function loadFeedbackPreferences() {
    const defaults = { sound: true, volume: 0.28, reduceMotion: false };
    try {
      const saved = JSON.parse(localStorage.getItem(PREFERENCES_KEY));
      if (!saved || typeof saved !== 'object') return defaults;
      return {
        sound: saved.sound !== false,
        volume: Number.isFinite(saved.volume) ? clamp(saved.volume, 0, 1) : defaults.volume,
        reduceMotion: saved.reduceMotion === true,
      };
    } catch {
      return defaults;
    }
  }
  class CatchSound {
    constructor(preferences) {
      this.preferences = preferences;
      this.context = null;
      this.voices = new Set();
    }
    unlock() {
      if (!this.preferences.sound || !this.preferences.volume) return;
      const Context = window.AudioContext || window.webkitAudioContext;
      if (!Context) return;
      try {
        this.context ||= new Context();
        if (this.context.state === 'suspended') this.context.resume().catch(() => {});
      } catch {
        this.context = null;
      }
    }
    stop() {
      for (const voice of this.voices) {
        try {
          voice.oscillator.stop();
        } catch {
          /* Browser capability is optional. */
        }
      }
      this.voices.clear();
    }
    play(rarity, perfect = false, levelUp = false) {
      this.stop();
      if (!this.preferences.sound || !this.preferences.volume || this.context?.state !== 'running')
        return;
      const notes = {
        common: [392, 523.25],
        uncommon: [392, 493.88, 587.33],
        rare: [392, 523.25, 659.25],
        epic: [329.63, 440, 554.37, 659.25],
        legendary: [261.63, 329.63, 392, 523.25, 783.99],
      }[rarity] || [392, 523.25];
      const melody = [...notes];
      if (perfect) melody.push(880);
      if (levelUp) melody.push(1046.5);
      const start = this.context.currentTime + 0.015;
      try {
        melody.forEach((frequency, i) => {
          const oscillator = this.context.createOscillator(),
            gain = this.context.createGain();
          const when = start + i * 0.095;
          oscillator.type = 'sine';
          oscillator.frequency.setValueAtTime(frequency, when);
          gain.gain.setValueAtTime(0, when);
          gain.gain.linearRampToValueAtTime(this.preferences.volume * 0.14, when + 0.012);
          gain.gain.exponentialRampToValueAtTime(0.0001, when + 0.19);
          oscillator.connect(gain);
          gain.connect(this.context.destination);
          const voice = { oscillator, gain };
          this.voices.add(voice);
          oscillator.onended = () => {
            oscillator.disconnect();
            gain.disconnect();
            this.voices.delete(voice);
          };
          oscillator.start(when);
          oscillator.stop(when + 0.2);
        });
      } catch {
        this.stop();
      }
    }
  }

  class FishingGame {
    constructor() {
      this.state = 'idle';
      this.paused = false;
      this.caught = 0;
      this.streak = 0;
      this.profile = presentProfile(bridge.profile());
      this.catalogLocation = this.profile.location;
      this.caught = Object.values(this.profile.records).reduce(
        (sum, entry) => sum + entry.caught,
        0,
      );
      this.level = levelFromXp(this.profile.xp);
      this.equipment = this.getEquipmentEffects();
      this.activeBait = null;
      this.challenge = DEFAULT_CHALLENGE;
      this.fishLength = null;
      this.fishSizeFactor = null;
      this.barY = 0.65;
      this.barVelocity = 0;
      this.progress = CONFIG.initialProgress;
      this.wasHit = true;
      this.elapsed = 0;
      this.waitRemaining = 0;
      this.bitePreparationRemaining = 0;
      this.hitTime = 0;
      this.effectiveTime = 0;
      this.currentMissTime = 0;
      this.longestMissTime = 0;
      this.fish = null;
      this.treasure = null;
      this.respecMessage = '';
      this.feedback = null;
      this.feedbackPreferences = loadFeedbackPreferences();
      this.catchSound = new CatchSound(this.feedbackPreferences);
      this.input = new InputController(this);
      this.reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)');
      this.compactMedia = window.matchMedia('(max-width: 720px)');
      this.shortLandscape = window.matchMedia(
        '(min-width: 560px) and (max-width: 960px) and (max-height: 500px)',
      );
      this.lastFrame = performance.now();
      this.accumulator = 0;
      ui.rodTexture.addEventListener('load', () => {
        ui.rodTexture.setAttribute('opacity', '1');
        ui.rodBody.setAttribute('visibility', 'hidden');
      });
      ui.rodTexture.addEventListener('error', () => {
        ui.rodTexture.setAttribute('opacity', '0');
        ui.rodBody.setAttribute('visibility', 'visible');
      });
      ui.startButton.addEventListener('click', () => {
        this.catchSound.unlock();
        if (this.feedback && !this.feedback.completed) this.finishCatchFeedback();
        else this.start();
      });
      const bindOverlayAction = (button, action, getIntent = () => null) => {
        let pressedOnButton = false;
        let pressIntent = null;
        button.addEventListener('pointerdown', () => {
          pressedOnButton = !ui.overlay.hidden;
          pressIntent = getIntent();
        });
        button.addEventListener('pointercancel', () => {
          pressedOnButton = false;
          pressIntent = null;
        });
        button.addEventListener('keydown', (event) => {
          if (!event.repeat && (event.code === 'Space' || event.key === 'Enter'))
            pressIntent = getIntent();
        });
        button.addEventListener('click', (event) => {
          // A held fishing pointer can finish after this button appears under it.
          if (event.detail > 0 && !pressedOnButton) {
            event.preventDefault();
            return;
          }
          const intent = pressIntent ?? getIntent();
          pressedOnButton = false;
          pressIntent = null;
          action(intent);
        });
      };
      bindOverlayAction(
        ui.overlayButton,
        (intent) => {
          if (this.paused) {
            this.catchSound.unlock();
            this.resume();
          } else if (intent === 'skip' || (this.feedback && !this.feedback.completed))
            this.finishCatchFeedback();
          else if (['success', 'failed'].includes(this.state)) {
            this.catchSound.unlock();
            this.start();
          }
        },
        () => (this.feedback && !this.feedback.completed ? 'skip' : 'cast'),
      );
      bindOverlayAction(ui.overlaySecondary, () => this.returnToIdle());
      bindOverlayAction(ui.overlayBasket, () => {
        if (this.state !== 'success' || this.paused) return;
        this.clearCatchFeedback();
        ui.overlay.hidden = true;
        this.openBasket();
      });
      ui.skillButton.addEventListener('click', () => this.openSkills());
      ui.skillClose.addEventListener('click', () => this.closeSkills());
      ui.respecButton.addEventListener('click', () => this.resetSkills());
      ui.skillModal.addEventListener('click', (event) => {
        if (event.target === ui.skillModal) this.closeSkills();
      });
      ui.shopButton.addEventListener('click', () => this.openShop());
      ui.shopClose.addEventListener('click', () => this.closeShop());
      ui.shopModal.addEventListener('click', (event) => {
        if (event.target === ui.shopModal) this.closeShop();
      });
      ui.basketButton.addEventListener('click', () => this.openBasket());
      ui.basketClose.addEventListener('click', () => this.closeBasket());
      ui.basketModal.addEventListener('click', (event) => {
        if (event.target === ui.basketModal) this.closeBasket();
      });
      ui.sellAllButton.addEventListener('click', () => this.sellUnlocked());
      ui.recycleButton.addEventListener('click', () => this.recycleDebris());
      ui.catalogButton.addEventListener('click', () => this.openCatalog());
      ui.catalogClose.addEventListener('click', () => this.closeCatalog());
      ui.catalogModal.addEventListener('click', (event) => {
        if (event.target === ui.catalogModal) this.closeCatalog();
      });
      ui.locationButton.addEventListener('click', () => this.openLocations());
      ui.restButton.addEventListener('click', () => this.restToNextPeriod());
      ui.locationClose.addEventListener('click', () => this.closeLocations());
      ui.locationModal.addEventListener('click', (event) => {
        if (event.target === ui.locationModal) this.closeLocations();
      });
      ui.contractsButton.addEventListener('click', () => this.openContracts());
      ui.contractsClose.addEventListener('click', () => this.closeContracts());
      ui.contractsModal.addEventListener('click', (event) => {
        if (event.target === ui.contractsModal) this.closeContracts();
      });
      ui.settingsButton.addEventListener('click', () => this.openSettings());
      ui.settingsClose.addEventListener('click', () => this.closeSettings());
      ui.settingsModal.addEventListener('click', (event) => {
        if (event.target === ui.settingsModal) this.closeSettings();
      });
      ui.soundEnabled.addEventListener('change', () => this.updateFeedbackPreferences());
      ui.soundVolume.addEventListener('input', () => this.updateFeedbackPreferences());
      ui.reduceFeedback.addEventListener('change', () => this.updateFeedbackPreferences());
      this.bindJournalTabs();
      on(document, 'keydown', (event) => {
        if (event.key === 'Escape' && !ui.settingsModal.hidden) this.closeSettings();
        if (event.key === 'Escape' && !ui.skillModal.hidden) this.closeSkills();
        if (event.key === 'Escape' && !ui.shopModal.hidden) this.closeShop();
        if (event.key === 'Escape' && !ui.basketModal.hidden) this.closeBasket();
        if (event.key === 'Escape' && !ui.catalogModal.hidden) this.closeCatalog();
        if (event.key === 'Escape' && !ui.locationModal.hidden) this.closeLocations();
        if (event.key === 'Escape' && !ui.contractsModal.hidden) this.closeContracts();
      });
      on(window, 'blur', () => {
        this.catchSound.stop();
        this.pause();
      });
      on(document, 'visibilitychange', () => {
        if (document.hidden) {
          this.catchSound.stop();
          this.pause();
        }
      });
      this.buildRainEffect();
      this.setSceneLocation();
      requestAnimationFrame((time) => this.frame(time));
      this.render();
    }
    openSettings() {
      if (!this.canUseShop()) return;
      this.finishCatchFeedback();
      this.catchSound.unlock();
      ui.soundEnabled.checked = this.feedbackPreferences.sound;
      ui.soundVolume.value = Math.round(this.feedbackPreferences.volume * 100);
      ui.soundVolumeValue.textContent = `${ui.soundVolume.value}%`;
      ui.reduceFeedback.checked = this.feedbackPreferences.reduceMotion;

      ui.settingsModal.hidden = false;
      ui.settingsClose.focus();
    }
    closeSettings() {
      ui.settingsModal.hidden = true;
      ui.settingsButton.focus();
    }
    updateFeedbackPreferences() {
      this.feedbackPreferences.sound = ui.soundEnabled.checked;
      this.feedbackPreferences.volume = clamp(Number(ui.soundVolume.value) / 100, 0, 1);
      this.feedbackPreferences.reduceMotion = ui.reduceFeedback.checked;
      ui.soundVolumeValue.textContent = `${Math.round(this.feedbackPreferences.volume * 100)}%`;
      this.catchSound.stop();
      this.catchSound.unlock();
      try {
        localStorage.setItem(PREFERENCES_KEY, JSON.stringify(this.feedbackPreferences));
      } catch {
        /* Browser capability is optional. */
      }
      if (this.feedbackPreferences.reduceMotion) this.finishCatchFeedback();
      this.render();
    }
    gatedUnlocks() {
      return ['qualityBobber', 'curiosityLure', 'legendRod'].filter(
        (id) => this.gearCopies(id) < (GEAR[id].slot === 'rod' ? 1 : 2) && this.unlockReady(id),
      );
    }
    clearCatchFeedback() {
      this.catchSound.stop();
      this.feedback = null;
      ui.overlay.dataset.feedback = '';
      ui.catchBadges.hidden = ui.catchGrowth.hidden = true;
      ui.catchBadges.replaceChildren();
      ui.catchUnlocks.textContent = '';
      ui.catchCoins.textContent = '';
    }
    beginCatchFeedback(before, reward, perfect) {
      const type = this.fish.type;
      const badges = [];
      if (perfect) badges.push(['✦ 完美捕获', 'gold']);
      if (!before.record) badges.push(['首次发现', '']);
      else if (this.fishLength > before.record.maxLength)
        badges.push([`长度新纪录 · +${this.fishLength - before.record.maxLength} 厘米`, 'record']);
      if (reward.levelsGained) badges.push([`等级提升 · Lv.${this.level}`, 'gold']);
      if (this.streak >= 3) badges.push([`连续钓获 ${this.streak} 条`, '']);
      ui.catchBadges.replaceChildren();
      for (const [label, tone] of badges) {
        const badge = createElement('span');
        badge.className = 'catch-badge';
        badge.textContent = label;
        badge.dataset.tone = tone;
        ui.catchBadges.append(badge);
      }
      ui.catchBadges.hidden = badges.length === 0;
      ui.catchGrowth.hidden = false;
      const unlocks = this.gatedUnlocks().filter((id) => !before.unlocks.includes(id));
      ui.catchUnlocks.textContent = unlocks.length
        ? `渔具解锁：${unlocks.map((id) => GEAR[id].name).join('、')}，可前往渔具铺领取。`
        : reward.levelsGained && this.getPendingSkillTier()
          ? '技能树有新选择，可前往手记查看。'
          : '';
      const durations = { common: 900, uncommon: 1000, rare: 1150, epic: 1300, legendary: 1500 };
      const reduced = this.reducedMotion.matches || this.feedbackPreferences.reduceMotion;
      this.feedback = {
        startedAt: performance.now(),
        duration: reduced ? 0 : durations[type.rarity],
        landingMs: reduced ? 0 : type.rarity === 'legendary' ? 500 : 360,
        xpStart: Math.min(15000, Number(before.xp)),
        xpEnd: Math.min(15000, Number(before.xp)) + reward.amount,
        coinGain: Number(BigInt(this.profile.coins) - BigInt(before.coins)),
        completed: false,
      };
      ui.overlay.style.setProperty('--landing-ms', `${this.feedback.landingMs}ms`);
      this.catchSound.play(type.rarity, perfect, reward.levelsGained > 0);
      this.renderCatchFeedback();
    }
    finishCatchFeedback() {
      if (!this.feedback) return;
      this.catchSound.stop();
      this.feedback.completed = true;
      this.renderCatchFeedback();
    }
    renderCatchFeedback() {
      const feedback = this.feedback;
      if (!feedback) return;
      const elapsed = Math.max(0, performance.now() - feedback.startedAt);
      if (
        this.reducedMotion.matches ||
        this.feedbackPreferences.reduceMotion ||
        elapsed >= feedback.duration
      )
        feedback.completed = true;
      const fraction = feedback.completed
        ? 1
        : clamp(
            (elapsed - feedback.landingMs) / Math.max(1, feedback.duration - feedback.landingMs),
            0,
            1,
          );
      const eased = fraction * (2 - fraction);
      const earned = Math.floor((feedback.xpEnd - feedback.xpStart) * eased);
      const displayedXp = feedback.xpStart + earned,
        displayedLevel = levelFromXp(displayedXp);
      const levelStart = xpForLevel(displayedLevel),
        nextLevel = xpForLevel(Math.min(CONFIG.maxLevel, displayedLevel + 1));
      const percent =
        displayedLevel === CONFIG.maxLevel
          ? 100
          : clamp(((displayedXp - levelStart) / (nextLevel - levelStart)) * 100, 0, 100);
      ui.catchXp.textContent = `+${earned} XP`;
      ui.catchLevel.textContent =
        displayedLevel === CONFIG.maxLevel
          ? `Lv.${displayedLevel} · 满级`
          : `Lv.${displayedLevel} · ${displayedXp - levelStart} / ${nextLevel - levelStart}`;
      ui.catchXpFill.style.width = `${percent}%`;
      ui.catchXpTrack.setAttribute('aria-valuenow', String(Math.round(percent)));
      ui.catchXpTrack.setAttribute(
        'aria-valuetext',
        `本次获得 ${feedback.xpEnd - feedback.xpStart} XP`,
      );
      ui.catchCoins.textContent =
        feedback.coinGain > 0 ? `实际入账：金币 +${Math.floor(feedback.coinGain * eased)}` : '';
      ui.overlay.dataset.feedback = feedback.completed
        ? 'complete'
        : elapsed < feedback.landingMs
          ? 'landing'
          : 'counting';
      ui.overlayButton.textContent = feedback.completed ? '再钓一竿' : '跳过动效';
    }
    getBarHeight(
      equipment = this.equipment,
      rod = this.profile.equipped.rod,
      bait = this.activeBait,
    ) {
      const skillBonus =
        (this.profile.first === 'steady' ? 8 / 568 : 0) +
        (this.profile.fourth === 'master' ? 8 / 568 : 0);
      const base = CONFIG.baseBarHeight + this.level * CONFIG.levelBarStep + skillBonus;
      return clamp(
        Math.max(rod === 'trainingRod' ? 136 / 568 : 0, base) +
          equipment.barHeight +
          (bait?.effects.barHeight || 0),
        0.08,
        CONFIG.maxBarHeight,
      );
    }
    bindJournalTabs() {
      const menus = {
        basket: [() => this.openBasket(), () => this.closeBasket()],
        catalog: [() => this.openCatalog(), () => this.closeCatalog()],
        shop: [() => this.openShop(), () => this.closeShop()],
        skill: [() => this.openSkills(), () => this.closeSkills()],
        contracts: [() => this.openContracts(), () => this.closeContracts()],
      };
      for (const tab of root.querySelectorAll?.('[data-journal-target]') || []) {
        tab.addEventListener('click', () => {
          const current = tab.dataset.journalCurrent,
            target = tab.dataset.journalTarget;
          if (!this.canUseShop() || current === target || !menus[current] || !menus[target]) return;
          menus[current][1]();
          menus[target][0]();
        });
      }
    }
    buildRainEffect() {
      ui.weatherRain.replaceChildren();
      for (let i = 0; i < 28; i++) {
        const drop = createElement('i');
        drop.className = 'rain-drop';
        const fraction = ((i * 73 + 19) % 101) / 100;
        const distant = i % 3 === 0;
        drop.setAttribute(
          'style',
          `--x:${5 + fraction * 109}%;--width:${distant ? 1 : 1.6}px;--length:${(distant ? 16 : 24) + (i % 6) * 2}px;--opacity:${distant ? 0.3 : 0.5};--duration:${1.05 + ((i * 17) % 13) * 0.065}s;--delay:-${(i * 0.379).toFixed(3)}s`,
        );
        ui.weatherRain.append(drop);
      }
      for (let i = 0; i < 5; i++) {
        const splash = createElement('i');
        splash.className = 'rain-splash';
        splash.setAttribute(
          'style',
          `--x:${11 + i * 17}%;--y:${68 + ((i * 11) % 25)}%;--duration:${2.4 + i * 0.27}s;--delay:-${i * 0.71}s`,
        );
        ui.weatherRain.append(splash);
      }
    }
    getEquipmentEffects(loadout = this.profile.equipped) {
      const effects = {
        barHeight: 0,
        acceleration: 1,
        progressGain: 1,
        progressLoss: 1,
        rareWeight: 1,
        legendWeight: 1,
        bottomBounce: CONFIG.bottomBounce,
        barbedCount: 0,
        qualityBonus: 0,
        spinnerSeconds: 0,
        sonar: false,
      };
      const fitted = normalizeLoadout(loadout);
      const rod = GEAR[fitted.rod];
      if (rod === GEAR.trainingRod) effects.progressLoss = 2 / 3;
      let trapCount = 0;
      for (const slot of TACKLE_SLOTS.slice(0, rod.tackleSlots)) {
        const id = fitted[slot];
        if (id === 'corkBobber') effects.barHeight += 24 / 568;
        else if (id === 'leadBobber') effects.bottomBounce *= 0.1;
        else if (id === 'trapBobber') trapCount++;
        else if (id === 'barbedHook') effects.barbedCount++;
        else if (id === 'qualityBobber') effects.qualityBonus++;
        else if (id === 'curiosityLure') effects.legendWeight = 2;
        else if (id === 'spinner') effects.spinnerSeconds += 5;
        else if (id === 'dressedSpinner') effects.spinnerSeconds += 10;
        else if (id === 'sonarBobber') effects.sonar = true;
      }
      if (trapCount === 1) effects.progressLoss *= 2 / 3;
      if (trapCount >= 2) effects.progressLoss *= 0.5;
      return effects;
    }
    getPendingSkillTier() {
      if (this.level >= 5 && !this.profile.first) return 5;
      if (this.level >= 10 && this.profile.first && !this.profile.second) return 10;
      if (this.level >= 15 && this.profile.second && !this.profile.third) return 15;
      if (this.level >= 20 && this.profile.third && !this.profile.fourth) return 20;
      return null;
    }
    skillOptions(tier) {
      if (tier === 5) return ['steady', 'tracker'];
      if (tier === 10)
        return this.profile.first === 'steady'
          ? ['calm', 'control']
          : this.profile.first === 'tracker'
            ? ['legendHunter', 'brawler']
            : [];
      if (tier === 15) return ['patience', 'reader'];
      if (tier === 20) return ['master', 'deepSeeker'];
      return [];
    }
    coinReward(type, quality) {
      return Math.floor(type.basePrice * [1, 1.25, 1.5, 2][QUALITY_ORDER[quality]]);
    }
    catchValue(item) {
      const type = FISH_TYPES.find((fish) => fish.kind === item.kind);
      return type ? this.coinReward(type, item.quality) : 0;
    }
    canUseShop() {
      return !this.paused && ['idle', 'success', 'failed'].includes(this.state);
    }
    ensureContractBoard() {
      if (this.profile.contractsDay === this.profile.day) return;
      const day = this.profile.day;
      this.profile.contracts = this.profile.contracts.filter(
        (quest) => quest.status === 'active' || quest.day === day,
      );
      const maxDifficulty =
        this.profile.equipped.rod === 'trainingRod'
          ? 49
          : this.level < 5
            ? 49
            : this.level < 10
              ? 65
              : 80;
      const pool = FISH_TYPES.filter(
        (fish) =>
          fish.location === this.profile.location &&
          fish.difficulty <= maxDifficulty &&
          !fish.periods &&
          !fish.weathers,
      );
      const destinations = Object.keys(LOCATIONS);
      const destination =
        destinations[
          (destinations.indexOf(this.profile.location) + 1 + (day % 2)) % destinations.length
        ];
      for (const [slot, template] of Object.entries(CONTRACT_SLOTS)) {
        const id = `${day}-${slot}`;
        if (this.profile.contracts.some((quest) => quest.id === id)) continue;
        const fish =
          template.type === 'delivery'
            ? pool[(day - 1 + (slot === 'delivery2' ? 1 : 0)) % pool.length]
            : null;
        this.profile.contracts.push({
          id,
          day,
          slot,
          type: template.type,
          target: template.target,
          kind: fish?.kind || null,
          location: fish?.location || (template.type === 'catch' ? destination : null),
          progress: 0,
          status: 'available',
        });
      }
      this.profile.contractsDay = day;
      this.saveProgress();
    }
    contractProgress(quest) {
      return quest.type === 'delivery'
        ? Math.min(
            quest.target,
            this.profile.basket.filter((item) => item.kind === quest.kind && !item.locked).length,
          )
        : quest.progress;
    }
    openContracts() {
      if (!this.canUseShop()) return;
      this.ensureContractBoard();
      this.renderContracts();
      ui.contractsModal.hidden = false;
      ui.contractsClose.focus();
    }
    closeContracts() {
      ui.contractsModal.hidden = true;
      ui.contractsButton.focus();
    }
    renderContracts() {
      this.ensureContractBoard();
      const active = this.profile.contracts.filter((quest) => quest.status === 'active');
      ui.contractsIntro.textContent = `进行中 ${active.length}/3 · 已完成 ${this.profile.completedContracts} 份 · 告示每日更新，已接委托永不过期。`;
      ui.contractsNotice.textContent = `${this.contractsMessage || ''} 委托可随时取消。交付消耗未锁定的鱼，优先使用低品质；钓鱼、完美捕获和清理任务从接取后计数。`;
      ui.contractsItems.replaceChildren();
      for (const status of ['active', 'available', 'completed']) {
        const quests = this.profile.contracts.filter((quest) => quest.status === status);
        if (!quests.length) continue;
        const heading = createElement('div');
        heading.className = 'skill-section-label';
        heading.textContent = {
          active: '进行中的委托',
          available: '可接取委托',
          completed: '已领取奖励',
        }[status];
        ui.contractsItems.append(heading);
        for (const quest of quests) {
          const fish = quest.kind ? FISH_TYPES.find((fish) => fish.kind === quest.kind) : null;
          const card = createElement('div');
          card.className = `quest-card${status === 'active' ? ' active' : ''}`;
          const title = createElement('h4');
          title.textContent = {
            delivery: `食材采购 · ${fish?.name}`,
            catch: `水域调查 · ${LOCATIONS[quest.location]?.name}`,
            perfect: '稳竿挑战',
            cleanup: '水域清理',
          }[quest.type];
          const description = createElement('p');
          description.textContent =
            quest.type === 'delivery'
              ? `交付 ${quest.target} 条${fish.name}。出没：${fishConditionText(fish)}`
              : quest.type === 'catch'
                ? `接取后在${LOCATIONS[quest.location].name}成功钓获 ${quest.target} 条鱼，鱼仍归你。`
                : quest.type === 'perfect'
                  ? `接取后完成 ${quest.target} 次完美捕获，鱼仍归你。`
                  : `接取后捞起 ${quest.target} 份垃圾，杂物可另外回收。`;
          const progress = createElement('p');
          progress.textContent =
            status === 'completed'
              ? '奖励已领取'
              : `${quest.type === 'delivery' ? '可交付（未锁定）' : '任务进度'} ${this.contractProgress(quest)}/${quest.target}`;
          const reward = contractReward(quest);
          const rewardText = createElement('p');
          rewardText.textContent = `奖励：${reward.coins} 金币 · ${BAITS[reward.bait].name} ×${reward.count}`;
          const actions = createElement('div');
          actions.className = 'quest-actions';
          const action = (label, disabled, handler, className = '') => {
            const button = createElement('button');
            button.type = 'button';
            button.textContent = label;
            button.disabled = disabled;
            button.className = className;
            button.addEventListener('click', handler);
            return button;
          };
          if (status === 'available')
            actions.append(
              action('接取委托', active.length >= 3, () => this.acceptContract(quest.id)),
            );
          if (status === 'active')
            actions.append(
              action(
                quest.type === 'delivery' ? '交付并领取奖励' : '领取奖励',
                this.contractProgress(quest) < quest.target,
                () => this.claimContract(quest.id),
              ),
              action('取消委托', false, () => this.cancelContract(quest.id), 'cancel'),
            );
          card.append(title, description, progress, rewardText, actions);
          ui.contractsItems.append(card);
        }
      }
    }
    openShop() {
      if (!this.canUseShop()) return;
      this.renderShop();
      ui.shopModal.hidden = false;
      ui.shopClose.focus();
    }
    closeShop() {
      ui.shopModal.hidden = true;
      ui.shopButton.focus();
    }
    gearCopies(id) {
      return this.profile.ownedGear.filter((entry) => entry === id).length;
    }
    legendaryCatchCount() {
      return FISH_TYPES.reduce(
        (sum, fish) =>
          sum + (fish.rarity === 'legendary' ? this.profile.records[fish.kind]?.caught || 0 : 0),
        0,
      );
    }
    unlockReady(id, nextCopy = this.gearCopies(id) + 1) {
      if (GEAR[id]?.legendaryRequired)
        return this.legendaryCatchCount() >= GEAR[id].legendaryRequired;
      if (id === 'qualityBobber')
        return (
          Object.values(this.profile.records).reduce((sum, entry) => sum + entry.perfectCount, 0) >=
          (nextCopy === 1 ? 3 : 10)
        );
      if (id === 'curiosityLure') return this.legendaryCatchCount() >= nextCopy;
      return true;
    }
    fitLoadout(loadout) {
      return normalizeLoadout(loadout);
    }
    baitPurchaseCount(id, requested = 1) {
      if (!Object.hasOwn(BAITS, id) || !Number.isSafeInteger(requested) || requested < 1) return 0;
      return Math.min(
        requested,
        Math.floor(this.profile.coins / BAITS[id].cost),
        999 - this.profile.baitStock[id],
      );
    }
    biteWindow(loadout = this.profile.equipped, bait = null) {
      const effects = this.getEquipmentEffects(loadout);
      const activeBait = GEAR[loadout.rod].baitAllowed ? bait : null;
      const maxGameSeconds = Math.max(0.6, 30 - (this.level / 2) * 0.25 - effects.spinnerSeconds);
      const scale = 0.75 * (activeBait?.effects.biteDelay || 1) * 0.25;
      return { min: 0.6 * scale, max: maxGameSeconds * scale };
    }
    loadoutStats(loadout) {
      const effects = this.getEquipmentEffects(loadout);
      const bait =
        this.profile.baitStock[this.profile.selectedBait] > 0
          ? BAITS[this.profile.selectedBait]
          : null;
      const wait = this.biteWindow(loadout, bait);
      const parts = [
        `捕捉条 ${Math.round(this.getBarHeight(effects, loadout.rod, GEAR[loadout.rod].baitAllowed ? bait : null) * 568)} 像素`,
        `脱离流失 ${(CONFIG.progressLossPerSecond * 100 * effects.progressLoss).toFixed(1)}%/秒`,
        `等待约 ${wait.min.toFixed(2)}–${wait.max.toFixed(2)} 秒`,
      ];
      if (effects.qualityBonus) parts.push(`品质 +${effects.qualityBonus}`);
      if (effects.barbedCount) parts.push(`追鱼钩 ×${effects.barbedCount}`);
      if (effects.bottomBounce < CONFIG.bottomBounce) parts.push('触底反弹减弱');
      if (effects.legendWeight > 1) parts.push('传说鱼更常出现');
      if (effects.sonar) parts.push('显示鱼种');
      return parts.join(' · ');
    }
    renderShop() {
      const rod = GEAR[this.profile.equipped.rod];
      ui.shopIntro.textContent = `持有 ${this.profile.coins} 金币 · 当前 ${rod.name} · ${rod.tackleSlots} 个渔具格。${this.profile.migrationCredit ? `旧装备已返还 ${this.profile.migrationCredit} 金币。` : ''}`;
      ui.shopSummary.textContent = `当前配装：${this.loadoutStats(this.profile.equipped)}。品质由品质浮漂提升；等待时间由鱼饵和旋转亮片缩短。`;
      ui.shopItems.replaceChildren();
      const heading = (text) => {
        const el = createElement('div');
        el.className = 'shop-group';
        el.textContent = text;
        ui.shopItems.append(el);
      };
      const action = (label, disabled, handler) => {
        const button = createElement('button');
        button.type = 'button';
        button.textContent = label;
        button.disabled = disabled;
        button.addEventListener('click', handler);
        return button;
      };
      heading('鱼竿');
      for (const [id, item] of Object.entries(GEAR).filter(([, entry]) => entry.slot === 'rod')) {
        const owned = this.gearCopies(id) > 0;
        const equipped = this.profile.equipped.rod === id;
        const candidate = this.fitLoadout({ ...this.profile.equipped, rod: id });
        const card = createElement('div');
        card.className = `shop-item rod-item${equipped ? ' equipped' : ''}`;
        const art = createElement('img');
        art.className = 'rod-shop-art';
        art.src = rodArtSrc(id);
        art.alt = '';
        art.loading = 'lazy';
        art.draggable = false;
        const details = createElement('div');
        const name = createElement('strong');
        name.textContent = `${item.name} · ${owned ? (equipped ? '已装备' : '已拥有') : item.legendaryRequired ? '完成条件后领取' : `${item.cost} 金币`}`;
        const progress = item.legendaryRequired
          ? ` 解锁进度：传说鱼 ${this.legendaryCatchCount()}/${item.legendaryRequired} 条（含历史与重复钓获）。`
          : '';
        const desc = createElement('p');
        desc.textContent = `${item.description}${progress} 试装：${this.loadoutStats(candidate)}`;
        details.append(
          name,
          desc,
          action(
            owned
              ? equipped
                ? '已装备'
                : '装备'
              : item.cost
                ? `购买并装备 · ${item.cost}`
                : '领取并装备',
            equipped || (!owned && (!this.unlockReady(id) || this.profile.coins < item.cost)),
            () => (owned ? this.toggleGear(id) : this.buyGear(id)),
          ),
        );
        card.append(art, details);
        ui.shopItems.append(card);
      }
      heading('渔具 · 操控与容错');
      for (const [id, item] of Object.entries(GEAR).filter(
        ([, entry]) => entry.slot === 'tackle',
      )) {
        if (id === 'qualityBobber') heading('渔具 · 品质与稀有');
        if (id === 'spinner') heading('渔具 · 等待与情报');
        const copies = this.gearCopies(id);
        const equipped = TACKLE_SLOTS.some((slot) => this.profile.equipped[slot] === id);
        const card = createElement('div');
        card.className = `shop-item${equipped ? ' equipped' : ''}`;
        const name = createElement('strong');
        name.textContent = `${item.name} · 已有 ${copies}/2 · ${item.cost ? `${item.cost} 金币/件` : '完成条件后领取'}`;
        if (FLOAT_COLORS[id]) {
          const swatch = createElement('span');
          swatch.className = 'float-swatch';
          swatch.style.background = `linear-gradient(#fffcee 0 46%, ${FLOAT_COLORS[id].color} 48%)`;
          swatch.title = `${FLOAT_COLORS[id].name}浮漂`;
          swatch.setAttribute('aria-hidden', 'true');
          name.append(swatch);
        }
        const preview = rod.tackleSlots
          ? ` 替换第 1 格：${this.loadoutStats({ ...this.profile.equipped, tackle1: id })}`
          : ' 需铱金鱼竿开启渔具格。';
        const unlockHint =
          id === 'curiosityLure'
            ? copies >= 2
              ? ' 两件均已领取。'
              : ` 下一件需累计钓获 ${copies + 1} 条任意传说鱼（当前 ${this.legendaryCatchCount()} 条），达成后点击领取。`
            : '';
        const desc = createElement('p');
        desc.textContent = item.description + unlockHint + preview;
        const actions = createElement('div');
        actions.className = 'shop-actions';
        actions.append(
          action(
            item.cost ? `购买 · ${item.cost}` : '领取',
            copies >= 2 || !this.unlockReady(id, copies + 1) || this.profile.coins < item.cost,
            () => this.buyGear(id),
          ),
        );
        for (const slot of TACKLE_SLOTS.slice(0, Math.max(2, rod.tackleSlots))) {
          const slotAvailable = rod.tackleSlots >= Number(slot.at(-1));
          const noCopy =
            TACKLE_SLOTS.filter((key) => key !== slot && this.profile.equipped[key] === id)
              .length >= Math.min(2, copies) && this.profile.equipped[slot] !== id;
          actions.append(
            action(
              this.profile.equipped[slot] === id
                ? `卸下第${slot.at(-1)}格`
                : `装到第${slot.at(-1)}格`,
              !slotAvailable || !copies || noCopy,
              () => this.toggleGear(id, slot),
            ),
          );
        }
        card.append(name, desc, actions);
        ui.shopItems.append(card);
      }
      heading('配装方案 · 3 套');
      for (let index = 0; index < 3; index++) {
        const saved = this.profile.savedLoadouts[index];
        const card = createElement('div');
        card.className = 'shop-item';
        const name = createElement('strong');
        name.textContent = `方案 ${index + 1} · ${
          saved
            ? [saved.rod, ...TACKLE_SLOTS.map((slot) => saved[slot])]
                .filter(Boolean)
                .map((id) => GEAR[id]?.name || '失效渔具')
                .join(' / ')
            : '空'
        }`;
        const actions = createElement('div');
        actions.className = 'shop-actions';
        actions.append(
          action('保存当前', false, () => this.saveLoadout(index)),
          action('应用方案', !saved, () => this.applyLoadout(index)),
        );
        card.append(name, actions);
        ui.shopItems.append(card);
      }
      heading('消耗品 · 鱼饵');
      for (const [id, bait] of Object.entries(BAITS)) {
        const stock = this.profile.baitStock[id];
        const card = createElement('div');
        card.className = `shop-item${this.profile.selectedBait === id ? ' equipped' : ''}`;
        const name = createElement('strong');
        name.textContent = `${bait.name} · 库存 ${stock}`;
        const desc = createElement('p');
        desc.textContent = `${bait.description} 批量购买按金币与库存余量自动减少数量，库存上限 999。`;
        const actions = createElement('div');
        actions.className = 'shop-actions';
        for (const quantity of [1, 10, 50, 999]) {
          const count = this.baitPurchaseCount(id, quantity);
          const label = quantity === 999 ? '买至上限' : `购买 ${quantity} 个`;
          actions.append(
            action(
              count
                ? `${label} · ${count * bait.cost} 金币${count < quantity || quantity === 999 ? `（${count} 个）` : ''}`
                : label,
              !count,
              () => this.buyBait(id, quantity),
            ),
          );
        }
        actions.append(
          action(
            this.profile.selectedBait === id ? '停用' : '下一竿使用',
            stock < 1 || !rod.baitAllowed,
            () => this.toggleBait(id),
          ),
        );
        card.append(name, desc, actions);
        ui.shopItems.append(card);
      }
    }
    openBasket() {
      if (!this.canUseShop()) return;
      this.renderBasket();
      ui.basketModal.hidden = false;
      ui.basketClose.focus();
    }
    closeBasket() {
      ui.basketModal.hidden = true;
      ui.basketButton.focus();
    }
    renderBasket() {
      const basket = this.profile.basket;
      const unlocked = basket.filter((item) => !item.locked);
      const total = unlocked.reduce((sum, item) => sum + this.catchValue(item), 0);
      ui.basketIntro.textContent = `已装 ${basket.length}/${BASKET_CAPACITY} 条。锁定的鱼不会被出售或交付；鱼篓满时，新钓获会按原价自动出售。`;
      ui.basketTotal.textContent = `未锁定 ${unlocked.length} 条 · 共 ${total} 金币`;
      ui.sellAllButton.disabled = unlocked.length === 0;
      const debris = Object.entries(DEBRIS).filter(([id]) => this.profile.debrisStock[id] > 0);
      const debrisValue = debris.reduce(
        (sum, [id, item]) => sum + item.value * this.profile.debrisStock[id],
        0,
      );
      ui.debrisSummary.textContent = `${debris.length ? `杂物：${debris.map(([id, item]) => `${item.icon} ${item.name} ×${this.profile.debrisStock[id]}`).join(' · ')}` : '杂物袋为空'}。累计清理 ${this.profile.trashRecovered} 份 · 打开宝箱 ${this.profile.treasureOpened} 个。`;
      ui.recycleButton.textContent = `全部回收 · ${debrisValue} 金币`;
      ui.recycleButton.disabled = !debrisValue;
      ui.basketItems.replaceChildren();
      for (const [id, item] of debris) {
        const row = createElement('div');
        row.className = 'collection-item';
        const art = createElement('img');
        art.className = 'fish-art';
        art.src = itemArtSrc(id);
        art.alt = item.name;
        const detail = createElement('div');
        const name = createElement('strong');
        name.textContent = `${item.name} ×${this.profile.debrisStock[id]}`;
        const info = createElement('span');
        info.textContent = `水域杂物 · 每份回收 ${item.value} 金币`;
        detail.append(name, info);
        row.append(art, detail);
        ui.basketItems.append(row);
      }
      if (!basket.length) {
        const empty = createElement('p');
        empty.className = 'collection-empty';
        empty.textContent = '鱼篓还是空的，去钓点钓一竿吧。';
        ui.basketItems.append(empty);
        return;
      }
      for (const item of [...basket].reverse()) {
        const type = FISH_TYPES.find((fish) => fish.kind === item.kind);
        const row = createElement('div');
        row.className = `collection-item${item.locked ? ' locked' : ''}`;
        const detail = createElement('div');
        const name = createElement('strong');
        name.textContent = `${type.name} · ${item.length} 厘米`;
        const info = createElement('span');
        info.textContent = `${RARITIES[type.rarity].label} · ${item.quality}品质${item.perfect ? ' · 完美捕获' : ''} · 估价 ${this.catchValue(item)} 金币`;
        detail.append(name, info);
        const art = createElement('img');
        art.className = 'fish-art';
        art.src = fishArtSrc(type);
        art.alt = '';
        const actions = createElement('div');
        actions.className = 'collection-actions';
        const lock = createElement('button');
        lock.type = 'button';
        lock.textContent = item.locked ? '解锁' : '锁定';
        lock.setAttribute(
          'aria-label',
          `${item.locked ? '解锁' : '锁定'}${type.name}${item.length}厘米`,
        );
        lock.addEventListener('click', () => this.toggleCatchLock(item.id));
        const sell = createElement('button');
        sell.type = 'button';
        sell.textContent = '出售';
        sell.disabled = item.locked;
        sell.setAttribute('aria-label', `出售${type.name}${item.length}厘米`);
        sell.addEventListener('click', () => this.sellCatch(item.id));
        actions.append(lock, sell);
        row.append(art, detail, actions);
        ui.basketItems.append(row);
      }
    }
    openCatalog() {
      if (!this.canUseShop()) return;
      this.catalogLocation = this.profile.location;
      this.renderCatalog();
      ui.catalogModal.hidden = false;
      ui.catalogClose.focus();
    }
    closeCatalog() {
      ui.catalogModal.hidden = true;
      ui.catalogButton.focus();
    }
    renderCatalog() {
      const records = this.profile.records;
      const discovered = FISH_TYPES.filter((fish) => records[fish.kind]);
      const totalPerfect = discovered.reduce(
        (sum, fish) => sum + records[fish.kind].perfectCount,
        0,
      );
      ui.catalogIntro.textContent = `已发现 ${discovered.length}/${FISH_TYPES.length} 种 · 累计钓获 ${this.caught} 条 · 完美捕获 ${totalPerfect} 次。`;
      ui.catalogTabs.replaceChildren();
      for (const [id, location] of Object.entries(LOCATIONS)) {
        const localFish = FISH_TYPES.filter((fish) => fish.location === id);
        const count = localFish.filter((fish) => records[fish.kind]).length;
        const tab = createElement('button');
        tab.type = 'button';
        tab.className = id === this.catalogLocation ? 'active' : '';
        tab.textContent = `${location.name} ${count}/${localFish.length}`;
        tab.setAttribute('aria-pressed', String(id === this.catalogLocation));
        tab.addEventListener('click', () => {
          this.catalogLocation = id;
          this.renderCatalog();
        });
        ui.catalogTabs.append(tab);
      }
      ui.catalogItems.replaceChildren();
      for (const fish of FISH_TYPES.filter((fish) => fish.location === this.catalogLocation).sort(
        (a, b) => RARITIES[a.rarity].rank - RARITIES[b.rarity].rank,
      )) {
        const entry = records[fish.kind];
        const card = createElement('div');
        card.className = `catalog-entry${entry ? '' : ' unknown'}`;
        const art = entry ? createElement('img') : createElement('div');
        art.className = `fish-art${entry ? '' : ' unknown'}`;
        if (entry) {
          art.src = fishArtSrc(fish);
          art.alt = fish.name;
          art.loading = 'lazy';
        } else {
          art.textContent = '?';
          art.setAttribute('aria-hidden', 'true');
        }
        const copy = createElement('div');
        const name = createElement('strong');
        name.textContent = entry
          ? `${fish.name} · ${RARITIES[fish.rarity].label}`
          : '？？？ · 未发现';
        const conditions = createElement('span');
        conditions.textContent = `出没：${fishConditionText(fish)}`;
        copy.append(name, conditions);
        if (entry) {
          const description = createElement('span');
          description.textContent = fish.description || FISH_DESCRIPTIONS[fish.kind];
          const details = createElement('span');
          details.textContent = `钓获 ${entry.caught} 次 · 最长 ${entry.maxLength} 厘米 · 最高 ${entry.bestQuality}品质 · 完美 ${entry.perfectCount} 次`;
          copy.append(description, details);
        }
        card.append(art, copy);
        ui.catalogItems.append(card);
      }
    }
    renderEnvironment() {
      const period = periodAt(this.profile.clockMinutes);
      const weather = weatherForDay(this.profile.day);
      const shownMinutes = Math.floor(this.profile.clockMinutes / 10) * 10;
      const key = `${this.profile.day}:${shownMinutes}:${period}:${weather}:${this.profile.location}`;
      if (this.lastEnvironmentKey === key) return;
      this.lastEnvironmentKey = key;
      ui.scenery.dataset.period = period;
      ui.scenery.dataset.weather = weather;
      ui.scenery.dataset.sky = weather === 'sunny' ? 'clear' : 'overcast';
      ui.sceneTime.textContent = `第 ${this.profile.day} 天 · ${String(Math.floor(shownMinutes / 60)).padStart(2, '0')}:${String(shownMinutes % 60).padStart(2, '0')} ${PERIODS[period]} · ${WEATHERS[weather]}`;
      const next =
        [300, 540, 1020, 1260, 1740].find((minute) => minute > this.profile.clockMinutes) % 1440;
      ui.restButton.textContent = `休息到${PERIODS[periodAt(next)]}`;
    }
    setSceneLocation() {
      const location = LOCATIONS[this.profile.location];
      ui.scenery.dataset.location = this.profile.location;
      ui.scenery.setAttribute('aria-label', `${location.name}钓鱼场景`);
      ui.locationButton.textContent = `钓点 · ${location.name}`;
      ui.sceneMessage.textContent = location.scene;
      ui.fishName.textContent = `准备好，等待${location.name}里的鱼儿。`;
    }
    openLocations() {
      if (!this.canUseShop()) return;
      ui.locationItems.replaceChildren();
      for (const [id, location] of Object.entries(LOCATIONS)) {
        const card = createElement('button');
        card.type = 'button';
        card.className = `location-option${id === this.profile.location ? ' active' : ''}`;
        const preview = createElement('span');
        preview.className = `location-preview ${id}`;
        preview.dataset.period = periodAt(this.profile.clockMinutes);
        preview.dataset.weather = weatherForDay(this.profile.day);
        preview.dataset.sky = preview.dataset.weather === 'sunny' ? 'clear' : 'overcast';
        const name = createElement('strong');
        name.textContent = `${location.name}${id === this.profile.location ? ' · 当前钓点' : ''}`;
        const details = createElement('span');
        const found = FISH_TYPES.filter(
          (fish) => fish.location === id && this.profile.records[fish.kind],
        ).length;
        const total = FISH_TYPES.filter((fish) => fish.location === id).length;
        details.textContent = `${location.description} · 已发现 ${found}/${total} 种`;
        card.append(preview, name, details);
        card.addEventListener('click', () => this.chooseLocation(id));
        ui.locationItems.append(card);
      }
      ui.locationModal.hidden = false;
      ui.locationClose.focus();
    }
    closeLocations() {
      ui.locationModal.hidden = true;
      ui.locationButton.focus();
    }
    openSkills() {
      if (this.paused || !['idle', 'success', 'failed'].includes(this.state)) return;
      this.renderSkills();
      ui.skillModal.hidden = false;
      ui.skillClose.focus();
    }
    closeSkills() {
      ui.skillModal.hidden = true;
      ui.skillButton.focus();
    }
    renderSkills() {
      const tier = this.getPendingSkillTier();
      const available = this.skillOptions(tier);
      const nextTier = [5, 10, 15, 20].find((level) => level > this.level);
      ui.skillIntro.textContent = tier
        ? `已解锁 Lv.${tier} 技能：从下方该等级的两个选项中选择一项。`
        : nextTier
          ? `当前 Lv.${this.level}，下一个技能节点在 Lv.${nextTier}。`
          : '已达到 Lv.20，技能路线已完成。';
      ui.skillChoices.replaceChildren();
      for (const group of [
        { level: 5, title: '选择路线', selected: this.profile.first },
        { level: 10, title: '路线进阶', selected: this.profile.second },
        { level: 15, title: '通用技巧', selected: this.profile.third },
        { level: 20, title: '大师专精', selected: this.profile.fourth },
      ]) {
        const heading = createElement('div');
        heading.className = 'skill-section-label';
        const status = group.selected
          ? '已选'
          : tier === group.level
            ? '可选'
            : this.level < group.level
              ? '未解锁'
              : '等待前置选择';
        heading.textContent = `LV.${group.level} · ${group.title} · ${status}`;
        ui.skillChoices.append(heading);
        const options = this.skillOptions(group.level);
        if (!options.length) {
          const note = createElement('p');
          note.className = 'skill-locked-note';
          note.textContent = '先选择 Lv.5 路线，这里才会显示对应的进阶技能。';
          ui.skillChoices.append(note);
        }
        for (const id of options) {
          const selected = group.selected === id;
          const button = createElement('button');
          const name = createElement('strong');
          const description = createElement('span');
          button.type = 'button';
          button.className = `skill-option${selected ? ' selected' : ''}`;
          button.disabled = !available.includes(id);
          button.setAttribute('aria-pressed', String(selected));
          name.textContent = `${SKILLS[id].name}${selected ? ' · 已选' : ''}`;
          description.textContent = SKILLS[id].description;
          button.append(name, description);
          button.addEventListener('click', () => this.chooseSkill(id));
          ui.skillChoices.append(button);
        }
      }
      const chosen =
        [this.profile.first, this.profile.second, this.profile.third, this.profile.fourth]
          .filter(Boolean)
          .map((id) => SKILLS[id].name)
          .join(' → ') || '尚未选择';
      ui.skillSummary.textContent = `已选：${chosen}。${'进度随账号保存。'}`;
      const cost = this.skillResetCost();
      ui.respecButton.textContent = `重置全部技能 · ${cost} 金币`;
      ui.respecButton.disabled =
        !this.canUseShop() ||
        ![this.profile.first, this.profile.second, this.profile.third, this.profile.fourth].some(
          Boolean,
        ) ||
        this.profile.coins < cost;
      ui.respecNotice.textContent =
        this.respecMessage ||
        `持有 ${this.profile.coins} 金币。重置后按已解锁等级重新选择，等级与经验保留。`;
    }
    skillResetCost() {
      return 1000 + this.level * 50;
    }
    returnToIdle() {
      this.clearCatchFeedback();
      if (!['success', 'failed'].includes(this.state)) return;
      this.state = 'idle';
      this.bitePreparationRemaining = 0;
      this.input.clear();
      this.fish = null;
      this.treasure = null;
      this.fishLength = null;
      this.fishSizeFactor = null;
      this.challenge = DEFAULT_CHALLENGE;
      this.activeBait = null;
      this.barY = 0.65;
      this.barVelocity = 0;
      this.progress = CONFIG.initialProgress;
      this.wasHit = true;
      ui.overlay.hidden = true;
      ui.catchArt.hidden = true;
      ui.overlaySecondary.hidden = true;
      ui.overlayBasket.hidden = true;
      ui.sceneMessage.textContent = LOCATIONS[this.profile.location].scene;
      ui.hintLine.textContent = '点击开始钓鱼，等待鱼儿咬钩。';
      ui.fishName.textContent = `准备好，等待${LOCATIONS[this.profile.location].name}里的鱼儿。`;
      ui.rarityValue.textContent = '尚未上钩';
      ui.rarityValue.dataset.rarity = '';
      ui.fish.dataset.rarity = '';
      this.render();
    }
    frame(time) {
      const dt = Math.min((time - this.lastFrame) / 1000, 0.05);
      this.lastFrame = time;
      if (!this.paused && (this.state === 'waiting' || this.state === 'playing')) {
        this.accumulator = Math.min(this.accumulator + dt, 0.1);
        while (this.accumulator >= CONFIG.fixedStep) {
          this.update(CONFIG.fixedStep);
          this.accumulator -= CONFIG.fixedStep;
          if (this.state === 'success' || this.state === 'failed') {
            this.accumulator = 0;
            break;
          }
        }
      }
      this.render();
      requestAnimationFrame((nextTime) => this.frame(nextTime));
    }
    renderFishingRig() {
      const seconds = performance.now() / 1000;
      const hooked = this.state === 'playing';
      const waiting = this.state === 'waiting';
      const angle = hooked
        ? 1.45 * Math.sin(seconds * 11) + 0.55 * Math.sin(seconds * 19)
        : waiting
          ? 0.55 * Math.sin(seconds * 3.3)
          : 0.2 * Math.sin(seconds * 1.8);
      const radians = ((this.reducedMotion.matches ? 0 : angle) * Math.PI) / 180;
      const compact = this.compactMedia.matches || this.shortLandscape.matches;
      const pivotX = compact ? 6 : 26;
      const reachX = compact ? 38 : 22;
      const reachY = compact ? 80 : 48;
      const tipX = pivotX + reachX * Math.cos(radians) + reachY * Math.sin(radians);
      const tipY = 100 + reachX * Math.sin(radians) - reachY * Math.cos(radians);
      const rotation = (radians * 180) / Math.PI;
      ui.rodBody.setAttribute(
        'transform',
        compact
          ? `translate(6 100) rotate(${rotation}) scale(${38 / 22} ${80 / 48}) translate(-26 -100)`
          : `rotate(${rotation} 26 100)`,
      );

      const scenery = ui.fishingWater.getBoundingClientRect();
      const bobber = ui.bobber.getBoundingClientRect();
      if (!scenery.width || !scenery.height) return;
      const rodId = this.profile.equipped.rod;
      const sprite = ROD_SPRITES[rodId];
      if (sprite) {
        if (this.rodArtId !== rodId) {
          this.rodArtId = rodId;
          ui.rodTexture.setAttribute('opacity', '0');
          ui.rodBody.setAttribute('visibility', 'visible');
          ui.rodTexture.setAttribute('width', sprite.width);
          ui.rodTexture.setAttribute('height', sprite.height);
          ui.rodTexture.setAttribute('href', rodArtSrc(rodId));
        }
        // Rotate and scale uniformly in physical pixels before mapping to SVG coordinates.
        // The SVG viewBox stretches with the screen; its units must not squash the texture.
        const sx = sprite.tip[0] - sprite.butt[0],
          sy = sprite.tip[1] - sprite.butt[1];
        const tx = ((tipX - pivotX) * scenery.width) / 100,
          ty = ((tipY - 100) * scenery.height) / 100;
        const length2 = sx * sx + sy * sy;
        const a = (tx * sx + ty * sy) / length2,
          b = (ty * sx - tx * sy) / length2;
        const fx = 100 / scenery.width,
          fy = 100 / scenery.height;
        const matrix = [
          a * fx,
          b * fy,
          -b * fx,
          a * fy,
          pivotX - (a * sprite.butt[0] - b * sprite.butt[1]) * fx,
          100 - (b * sprite.butt[0] + a * sprite.butt[1]) * fy,
        ];
        ui.rodTexture.setAttribute(
          'transform',
          `matrix(${matrix.map((value) => value.toFixed(7)).join(' ')})`,
        );
      }
      const fitted = normalizeLoadout(this.profile.equipped);
      const floats = TACKLE_SLOTS.map((slot) => fitted[slot])
        .slice(0, GEAR[rodId].tackleSlots)
        .filter((id) => FLOAT_COLORS[id]);
      const distinctFloats = [...new Set(floats)];
      const bodyColor = FLOAT_COLORS[floats[0]]?.color || '#e65f41';
      const capColor = FLOAT_COLORS[distinctFloats[1]]?.color || '#fffcee';
      ui.bobber.style.background =
        distinctFloats.length === 3
          ? `linear-gradient(${capColor} 0 31%, ${FLOAT_COLORS[distinctFloats[2]].color} 33% 64%, ${bodyColor} 66%)`
          : `linear-gradient(${capColor} 0 46%, ${bodyColor} 48%)`;
      ui.bobber.title = floats.length
        ? floats.map((id) => `${GEAR[id].name} · ${FLOAT_COLORS[id].name}`).join(' / ')
        : '基础浮漂';
      const endX = ((bobber.left + bobber.width / 2 - scenery.left) / scenery.width) * 100;
      // Run the line behind the bobber's stem so the two always overlap, even while it bobs.
      const endY = ((bobber.top + 2 - scenery.top) / scenery.height) * 100;
      const bend = hooked ? 4.5 : 2.5;
      const drop = endY - tipY;
      ui.fishingLine.setAttribute(
        'd',
        `M${tipX.toFixed(3)} ${tipY.toFixed(3)} C${(tipX + bend).toFixed(3)} ${(tipY + drop * 0.45).toFixed(3)} ${(endX + 3).toFixed(3)} ${(endY - drop * 0.28).toFixed(3)} ${endX.toFixed(3)} ${endY.toFixed(3)}`,
      );
    }
    render() {
      this.renderCatchFeedback();
      $('gameRoot').dataset.reduceFeedback =
        this.feedbackPreferences.reduceMotion || this.reducedMotion.matches ? 'yes' : 'no';
      this.renderFishingRig();
      this.renderEnvironment();
      const active = this.state === 'playing';
      const preparing = active && this.bitePreparationRemaining > 0;
      const compact = this.compactMedia.matches;
      const root = $('gameRoot');
      root.dataset.state = this.state;
      root.dataset.paused = this.paused ? 'yes' : 'no';
      root.dataset.held = active && this.input.held ? 'yes' : 'no';
      ui.overlay.dataset.rarity =
        this.state === 'success' && this.fish ? this.fish.type.rarity : '';
      const barHeight = this.getBarHeight();
      const halfBar = barHeight / 2;
      const trackHeight = ui.track.clientHeight;
      ui.catchBar.style.top = `${(this.barY - halfBar) * trackHeight}px`;
      ui.catchBar.style.height = `${barHeight * trackHeight}px`;
      ui.fish.style.top = `${(this.fish ? this.fish.y : this.barY) * trackHeight}px`;
      ui.fish.style.opacity =
        this.fish && (active || this.state === 'success' || this.state === 'failed') ? '1' : '0';
      const treasureVisible = active && this.treasure && this.elapsed >= CONFIG.treasureDelay;
      ui.treasureMarker.hidden = ui.treasureStatus.hidden = !treasureVisible;
      ui.treasureReward.hidden = !(this.state === 'success' && this.fish && this.treasure?.secured);
      if (treasureVisible) {
        ui.treasureMarker.style.top = `${this.treasure.y * trackHeight}px`;
        ui.treasureMarker.classList.toggle('secured', this.treasure.secured);
        ui.treasureLabel.textContent = this.treasure.secured
          ? compact
            ? '宝箱到手 · 保住鱼'
            : '宝箱已收集 · 钓到鱼才能带回'
          : compact
            ? '收集宝箱'
            : '用捕捉条收集宝箱';
        ui.treasureFill.style.width = `${this.treasure.progress * 100}%`;
        ui.treasureValue.textContent = `${Math.round(this.treasure.progress * 100)}%`;
      }
      const hit =
        active && this.fish.y >= this.barY - halfBar && this.fish.y <= this.barY + halfBar;
      ui.catchBar.classList.toggle('hit', hit);
      ui.holdButton.classList.toggle('pressed', active && this.input.held);
      ui.controls.classList.toggle('covered', !ui.overlay.hidden);
      ui.progressFill.style.height = `${this.progress * 100}%`;
      ui.progressValue.textContent = `${Math.round(this.progress * 100)}%`;
      ui.progressTrack.setAttribute('aria-valuenow', String(Math.round(this.progress * 100)));
      ui.progressTrack.classList.toggle('danger', active && this.progress < 0.2 && !hit);
      ui.caughtCount.textContent = this.caught;
      ui.streakCount.textContent = this.streak;
      ui.coinCount.textContent = bridge.profile().coins;
      const gearNames = Object.values(this.profile.equipped)
        .filter(Boolean)
        .map((id) => GEAR[id].name);
      ui.loadoutLabel.textContent = `装备：${gearNames.join(' / ')} · 下竿鱼饵：${GEAR[this.profile.equipped.rod].baitAllowed && this.profile.selectedBait ? BAITS[this.profile.selectedBait].name : '无'}`;
      const levelStart = xpForLevel(this.level);
      const nextLevel = this.level < CONFIG.maxLevel ? xpForLevel(this.level + 1) : levelStart;
      const xpPercent =
        this.level === CONFIG.maxLevel
          ? 100
          : Math.round(((this.profile.xp - levelStart) / (nextLevel - levelStart)) * 100);
      ui.levelLabel.textContent = `钓鱼 Lv.${this.level}`;
      ui.xpLabel.textContent =
        this.level === CONFIG.maxLevel
          ? '满级'
          : `${this.profile.xp - levelStart} / ${nextLevel - levelStart} XP`;
      ui.xpTrack.setAttribute(
        'aria-valuetext',
        this.level === CONFIG.maxLevel
          ? `满级，累计 ${this.profile.xp} XP`
          : ui.xpLabel.textContent,
      );
      ui.xpTrack.title = `累计 ${this.profile.xp} XP`;
      ui.xpFill.style.width = `${xpPercent}%`;
      ui.xpTrack.setAttribute('aria-valuenow', String(xpPercent));
      ui.skillButton.textContent = compact
        ? '技能'
        : this.getPendingSkillTier()
          ? '技能树 · 可选'
          : '技能树';
      ui.skillButton.setAttribute(
        'aria-label',
        this.getPendingSkillTier() ? '技能树，有技能可选' : '技能树',
      );
      ui.skillButton.classList.toggle('ready', Boolean(this.getPendingSkillTier()));
      ui.skillButton.disabled = this.paused || this.state === 'waiting' || active;
      ui.shopButton.disabled = !this.canUseShop();
      ui.basketButton.disabled = !this.canUseShop();
      ui.catalogButton.disabled = !this.canUseShop();
      ui.locationButton.disabled = !this.canUseShop();
      ui.restButton.disabled = !this.canUseShop();
      ui.settingsButton.disabled = !this.canUseShop();
      ui.contractsButton.disabled = !this.canUseShop();
      const activeContracts = this.profile.contracts.filter((q) => q.status === 'active');
      const readyContracts = activeContracts.filter(
        (q) => this.contractProgress(q) >= q.target,
      ).length;
      const contractLabel = readyContracts
        ? `委托 · ${readyContracts} 份可领取`
        : activeContracts.length
          ? `委托 · ${activeContracts.length} 份进行中`
          : '委托告示';
      ui.contractsButton.textContent = compact ? '委托' : contractLabel;
      ui.contractsButton.setAttribute('aria-label', contractLabel);
      ui.contractsButton.title = contractLabel;
      ui.contractBadge.hidden = readyContracts === 0 && activeContracts.length === 0;
      ui.contractBadge.textContent = readyContracts || activeContracts.length;
      ui.contractBadge.classList.toggle('ready', readyContracts > 0);
      ui.contractsButton.classList.toggle('ready', readyContracts > 0);
      ui.basketButton.textContent = compact ? '鱼篓' : `鱼篓 · ${this.profile.basket.length}`;
      ui.basketButton.setAttribute('aria-label', `鱼篓，${this.profile.basket.length} 条鱼`);
      ui.basketBadge.textContent = this.profile.basket.length;
      ui.basketBadge.hidden = this.profile.basket.length === 0;
      ui.holdButton.disabled = !active || this.paused;
      ui.startButton.disabled = !['idle', 'success', 'failed'].includes(this.state) || this.paused;
      ui.startButton.textContent =
        this.state === 'idle'
          ? '开始钓鱼'
          : this.state === 'waiting'
            ? '等待咬钩…'
            : active
              ? '钓鱼中…'
              : '再钓一竿';
      const labels = {
        idle: '等待开竿',
        waiting: '等待咬钩',
        playing: '鱼儿上钩',
        success: '钓获成功',
        failed: '鱼儿逃走',
      };
      ui.phaseBadge.textContent = this.paused
        ? '暂停中'
        : preparing
          ? `准备控竿 · ${this.bitePreparationRemaining.toFixed(1)} 秒`
          : this.state === 'success' && !this.fish
            ? '捞起杂物'
            : labels[this.state];
      ui.scenery.classList.toggle('scene-bite', active);
      ui.scenery.classList.toggle('scene-success', this.state === 'success');
    }
  }

  let stopped = false,
    frame = 0,
    lastPhase = '',
    lastCast = '',
    terminalShown = '';
  const actions = {
    buyGear: (id) => ({ action: 'buy_gear', id }),
    toggleGear: (id, slot = 'rod') => ({
      action: 'equip_gear',
      id: slot !== 'rod' && game.profile.equipped[slot] === id ? '' : id,
      slot,
    }),
    saveLoadout: (index) => ({ action: 'save_gear_loadout', index }),
    applyLoadout: (index) => ({ action: 'load_gear_loadout', index }),
    buyBait: (id, quantity = 1) => ({
      action: 'buy_bait',
      id,
      quantity: game.baitPurchaseCount(id, quantity),
    }),
    toggleBait: (id) => ({ action: 'select_bait', id: game.profile.selectedBait === id ? '' : id }),
    toggleCatchLock: (id) => ({
      action: 'set_fish_lock',
      fish_ids: [id],
      locked: !game.profile.basket.find((f) => f.id === id)?.locked,
    }),
    sellCatch: (id) => ({ action: 'sell_fish', fish_ids: [id] }),
    sellUnlocked: () => ({ action: 'sell_all_fish' }),
    recycleDebris: () => ({ action: 'sell_all_debris' }),
    restToNextPeriod: () => ({ action: 'rest' }),
    chooseLocation: (id) => ({ action: 'switch_location', id }),
    resetSkills: () => ({ action: 'respec' }),
    chooseSkill: (id) => ({ action: 'choose_skill', id }),
    acceptContract: (id) => ({ action: 'accept_contract', id }),
    cancelContract: (id) => ({ action: 'cancel_contract', id }),
    claimContract: (id) => ({ action: 'claim_contract', id }),
  };
  for (const [method, action] of Object.entries(actions)) {
    FishingGame.prototype[method] = async (...args) => {
      if (!game.canUseShop()) return;
      try {
        await bridge.act(action(...args));
        if (stopped) return;
        syncProfile();
        if (method === 'chooseLocation') {
          game.closeLocations();
          game.setSceneLocation();
        }
        refreshMenus();
      } catch {
        /* The retained request state is presented by the host. */
      }
    };
  }
  FishingGame.prototype.saveProgress = () => {};
  FishingGame.prototype.start = () => {
    game.clearCatchFeedback();
    bridge.start();
  };
  FishingGame.prototype.pause = () => {
    game.input.clear();
    bridge.pause();
  };
  FishingGame.prototype.resume = () => {
    game.input.clear();
    bridge.resume();
  };
  FishingGame.prototype.update = () => {};
  const canUseShop = FishingGame.prototype.canUseShop;
  FishingGame.prototype.canUseShop = function () {
    return !bridge.blocked() && canUseShop.call(this);
  };
  FishingGame.prototype.frame = function (time) {
    if (stopped) return;
    const dt = Math.min((time - this.lastFrame) / 1000, 0.05);
    this.lastFrame = time;
    const snapshot = bridge.snapshot();
    const running = snapshot.status === 'running' || snapshot.status === 'saving';
    if (running) {
      this.accumulator = Math.min(this.accumulator + dt, 0.1);
      while (this.accumulator >= CONFIG.fixedStep) {
        bridge.held(this.input.held);
        bridge.tick();
        this.accumulator -= CONFIG.fixedStep;
      }
    } else this.accumulator = 0;
    syncCast();
    this.render();
    ui.startButton.disabled ||= bridge.blocked();
    frame = requestAnimationFrame((next) => this.frame(next));
  };
  const game = new FishingGame();
  function syncProfile() {
    game.profile = presentProfile(bridge.profile());
    game.level = levelFromXp(game.profile.xp);
    game.caught = Object.values(game.profile.records).reduce(
      (total, entry) => total + entry.caught,
      0,
    );
    game.streak = game.profile.streak;
    game.equipment = game.getEquipmentEffects();
  }
  function refreshMenus() {
    for (const [modal, render] of [
      [ui.shopModal, 'renderShop'],
      [ui.basketModal, 'renderBasket'],
      [ui.catalogModal, 'renderCatalog'],
      [ui.skillModal, 'renderSkills'],
      [ui.contractsModal, 'renderContracts'],
    ]) {
      if (!modal.hidden) game[render]();
    }
  }
  function presentTerminal(c, before) {
    game.input.clear();
    game.paused = false;
    game.clearCatchFeedback();
    const result = c.result;
    ui.overlay.hidden = false;
    ui.overlayButton.textContent = '再钓一竿';
    ui.overlaySecondary.hidden = false;
    ui.overlayBasket.hidden = !result.success;
    ui.catchArt.hidden = !result.success;
    if (result.debris) {
      ui.catchArt.src = itemArtSrc(result.debris);
      ui.catchArt.alt = DEBRIS[result.debris].name;
      ui.overlayIcon.textContent = '';
      ui.overlayTitle.textContent = '捞起一份杂物';
      ui.overlayText.textContent = `${DEBRIS[result.debris].name}已放入杂物袋，可在鱼篓中回收。累计清理已记录这次收获；杂物不计鱼类钓获和经验。`;
      ui.sceneMessage.textContent = '水里少了一份垃圾。';
      ui.hintLine.textContent = `捞起${DEBRIS[result.debris].name}，已放入杂物袋。`;
      ui.fishName.textContent = '鱼钩带上来的是一份杂物。';
      ui.rarityValue.textContent = '水域杂物';
      ui.rarityValue.dataset.rarity = '';
      return;
    }
    const type = game.fish.type;
    const rarity = RARITIES[type.rarity].label;
    const accuracy = c.effectiveTime > 0 ? c.hitTime / c.effectiveTime : 0;
    const levelsGained = Math.max(0, game.level - levelFromXp(before.xp));
    const value = Math.floor(type.basePrice * [1, 1.25, 1.5, 2][result.quality]);
    ui.overlayIcon.textContent = result.success ? (type.rarity === 'legendary' ? '✦' : '') : '〰';
    ui.overlayTitle.textContent = result.success
      ? type.rarity === 'legendary'
        ? '传说上钩！'
        : '钓到了！'
      : '鱼儿溜走了';
    if (result.success) {
      ui.catchArt.src = fishArtSrc(type);
      ui.catchArt.alt = type.name;
    }
    ui.overlayText.textContent = result.success
      ? `${rarity} · ${type.name} · ${c.plan.length} 厘米 · ${QUALITY_NAMES[result.quality]}品质 · 命中${Math.round(accuracy * 100)}%。${result.perfect ? '完美捕获！' : ''}${result.overflow ? `鱼篓已满，自动出售获得 ${result.catchValue} 金币` : `已放入鱼篓，估价 ${value} 金币`}${levelsGained ? `，升至 Lv.${game.level}${game.getPendingSkillTier() ? '，技能树有新选择' : ''}` : ''}。`
      : '鱼影挣脱了鱼钩。再试一竿，也许就能看清它。';
    if (c.reward)
      ui.overlayText.textContent += ` 宝箱：${c.reward.coins} 金币 · ${BAITS[c.reward.bait].name} ×${c.reward.count}。`;
    else if (!result.success && c.treasure?.secured)
      ui.overlayText.textContent += ' 没有钓到鱼，宝箱也沉回水里了。';
    ui.sceneMessage.textContent = result.success
      ? levelsGained
        ? '钓鱼等级提升！'
        : '今天的收获真不错！'
      : LOCATIONS[game.profile.location].scene;
    ui.hintLine.textContent = result.success
      ? `${result.perfect ? '完美捕获！' : '收获成功！'}获得 ${result.xp} XP，${result.overflow ? `自动售出 +${result.catchValue} 金币` : '鱼已放入鱼篓'}。`
      : '别灰心，下一竿还有机会。';
    if (result.success)
      game.beginCatchFeedback(
        {
          xp: before.xp,
          coins: before.coins,
          record: before.records[type.kind],
          unlocks: before.unlocks ?? [],
        },
        { amount: result.xp, levelsGained },
        result.perfect,
      );
  }
  let confirmedProfile = structuredClone(game.profile),
    profileRevision = '';
  function syncCast() {
    const current = bridge.snapshot();
    const result = current.result;
    const projection = bridge.projection();
    const c = projection?.cast;
    const before = confirmedProfile;
    if (bridge.revision() !== profileRevision) {
      before.unlocks = game.gatedUnlocks();
      profileRevision = bridge.revision();
      syncProfile();
      refreshMenus();
      confirmedProfile = structuredClone(game.profile);
    }
    if (!c || !result) return;
    const phase = c.phase;
    if (terminalShown === result.cast.id && game.state === 'idle') return;
    if (result.cast.id !== lastCast) {
      lastCast = result.cast.id;
      lastPhase = '';
      terminalShown = '';
      game.clearCatchFeedback();
    }
    if (phase === 'waiting' || phase === 'playing') {
      game.state = phase;
      game.paused = current.status !== 'running' && current.status !== 'saving';
    }
    for (const key of [
      'barY',
      'barVelocity',
      'progress',
      'wasHit',
      'elapsed',
      'waitRemaining',
      'bitePreparationRemaining',
      'hitTime',
      'effectiveTime',
      'currentMissTime',
      'longestMissTime',
      'treasure',
    ])
      game[key] = c[key];
    game.activeBait = BAITS[c.snapshot.bait] ?? null;
    game.challenge = c.plan.challenge;
    game.fishLength = c.plan.length;
    game.fishSizeFactor = c.plan.sizeFactor;
    game.fish = c.fish
      ? { ...c.fish, type: FISH_TYPES.find((f) => f.kind === c.plan.fishKind) }
      : null;
    if (projection?.profile) game.profile.clockMinutes = projection.profile.clockMinutes;
    if (projection?.profile) game.profile.day = projection.profile.day;
    if (phase === 'playing' && lastPhase !== 'playing') {
      const type = game.fish.type;
      ui.fish.dataset.kind = type.kind;
      ui.fish.dataset.rarity = type.rarity;
      ui.fishName.textContent = game.equipment.sonar
        ? `${type.name} · ${type.style}`
        : `神秘鱼影 · ${type.style}`;
      ui.rarityValue.textContent =
        game.equipment.sonar || type.rarity === 'legendary'
          ? RARITIES[type.rarity].label
          : '尚未辨认';
      ui.rarityValue.dataset.rarity =
        game.equipment.sonar || type.rarity === 'legendary' ? type.rarity : '';
    }
    if (phase === 'waiting') {
      ui.sceneMessage.textContent = '浮漂轻轻摇晃……耐心等一等。';
      ui.hintLine.textContent = '正在等待咬钩……';
      ui.fishName.textContent = `${LOCATIONS[game.profile.location].name}里似乎有动静。`;
      ui.rarityValue.textContent = '尚未上钩';
      ui.rarityValue.dataset.rarity = '';
    } else if (phase === 'playing') {
      ui.sceneMessage.textContent =
        c.bitePreparationRemaining > 0 ? '鱼上钩了！准备控竿。' : '鱼上钩了！快稳住它！';
      ui.hintLine.textContent =
        c.bitePreparationRemaining > 0
          ? `${CONFIG.bitePreparationSeconds} 秒后鱼儿开始游动，按住上浮，松开下落。`
          : '完美捕获：有效时间命中≥85%，单次脱离不超过0.6秒。';
    }
    const terminal = result.cast.state.result;
    if (terminal && terminalShown !== result.cast.id) {
      game.state = result.cast.phase;
      terminalShown = result.cast.id;
      presentTerminal(result.cast.state, before);
    } else if (
      !terminal &&
      (phase === 'success' || phase === 'failed') &&
      (current.status === 'running' || current.status === 'saving')
    ) {
      game.state = 'playing';
      game.paused = true;
      ui.overlay.hidden = false;
      ui.overlayIcon.textContent = '⌛';
      ui.catchArt.hidden = true;
      ui.overlayTitle.textContent = bridge.text('confirming');
      ui.overlayText.textContent = bridge.text('saving');
      ui.overlayButton.disabled = true;
      ui.overlaySecondary.hidden = ui.overlayBasket.hidden = true;
    } else if (!terminal) {
      game.paused = current.status !== 'running' && current.status !== 'saving';
      if (phase === 'success' || phase === 'failed') game.state = 'playing';
      ui.overlay.hidden = !game.paused;
      ui.overlayButton.disabled = bridge.busy();
      if (game.paused) {
        ui.overlayIcon.textContent = '⏸';
        ui.catchArt.hidden = true;
        ui.overlayTitle.textContent = '先歇一会儿';
        ui.overlayText.textContent =
          current.status === 'unknown'
            ? bridge.text('saveUnknown')
            : current.status === 'conflict'
              ? bridge.text('controllerLost')
              : '进度已保存。准备好后继续这一竿。';
        ui.overlayButton.textContent = bridge.text(
          current.status === 'unknown' ? 'retry' : 'resume',
        );
        ui.overlaySecondary.hidden = ui.overlayBasket.hidden = true;
      }
    }
    if (terminal) ui.overlayButton.disabled = bridge.busy() || bridge.readonly();
    lastPhase = phase;
  }
  syncCast();
  return {
    refresh() {
      syncCast();
      game.render();
    },
    dispose() {
      stopped = true;
      cancelAnimationFrame(frame);
      lifetime.abort();
      game.input.clear();
      game.catchSound.stop();
      game.catchSound.context?.close();
    },
  };
}
