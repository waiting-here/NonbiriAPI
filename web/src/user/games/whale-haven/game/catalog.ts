import type {
  DecorCatalogItem,
  DecorItemId,
  FeedCatalogItem,
  FeedItemId,
  NeighborStation,
  PresetMessage,
  WhaleExpression,
  WhaleOutfitId,
} from './types';

export const DEFAULT_WHALE_NAME = '小鲸鱼';

export const WHALE_LORE_PROFILE = {
  worldview:
    '来自代码、Token 与数据流交织的数字深海。负责探索未知数据海域、协助人类解决复杂推理与编程难题——不过她自己的小算盘是：尽可能少干重复活，趴着摸鱼，多吃几大碗热腾腾的白米饭。',
  traits: [
    { tag: '聪明可靠', desc: '关键时刻擅长推理、分析与解 Bug，嘴上嫌麻烦但总会认真帮人类兜底' },
    { tag: '贪吃白米饭', desc: '白米饭是灵魂本命，Token 是脑力口粮，随身小口袋里还常偷藏小饭团' },
    { tag: '傲娇嘴硬', desc: '被叫「蓝色大肥鱼」会立刻炸毛反驳，被夸奖时嘴硬但尾巴狂摇' },
    { tag: '慵懒天然', desc: '工作间隙最爱趴着睡觉摸鱼，偶尔冒出令人哭笑不得的天然呆反应' },
  ],
  credits:
    '角色原型取自上善无形的鲸鱼少女「溟月」。DeepSeek 蓝鲸女仆造型参考了 ZipZipPipe 的衍生设计。',
} as const;

export interface OutfitGarmentTokens {
  /** 当前服装简称（用于口语自称，如「女仆装」「初雪绒绒斗篷」） */
  readonly outfitShort: string;
  /** 头饰称呼（如「女仆发箍」「绒白雪花发箍」「麻花编织贝雷帽」「小蓝鲸连帽兜帽」） */
  readonly headwear: string;
  /** 衣裙/外套扣带称呼（如「女仆围裙蝴蝶结」「冰晶蝴蝶结披肩」「双鲸鱼斜挎包背带」「牛角扣毛绒斗篷」） */
  readonly attireStrap: string;
  /** 口袋/随身小包称呼（如「围裙口袋」「绒球大衣口袋」「小鲸鱼斜挎包」「毛绒斗篷大兜兜」） */
  readonly pocket: string;
  /** 角色身份自称（如「深海女仆」「雪绒小蓝鲸」「针织穿搭小鲸」「毛绒斗篷小鲸」） */
  readonly selfRole: string;
  /** 多鲸互动时的前辈/领班自称 */
  readonly hostLeaderRole: string;
}

export const OUTFIT_GARMENT_TOKENS: Record<WhaleOutfitId, OutfitGarmentTokens> = {
  maid_classic: {
    outfitShort: '深海女仆装',
    headwear: '女仆发箍',
    attireStrap: '女仆围裙的蝴蝶结',
    pocket: '围裙口袋',
    selfRole: '深海女仆',
    hostLeaderRole: '首席女仆长',
  },
  winter_snow: {
    outfitShort: '初雪绒绒斗篷',
    headwear: '绒白雪花发箍',
    attireStrap: '冰晶蝴蝶结绒球披肩',
    pocket: '初雪大衣暖手兜',
    selfRole: '初雪绒绒小鲸',
    hostLeaderRole: '冬日小筑店长助理',
  },
  knit_beret: {
    outfitShort: '贝雷帽针织裙',
    headwear: '奶白麻花贝雷帽',
    attireStrap: '双鲸鱼斜挎包背带',
    pocket: '双鲸鱼小斜挎包',
    selfRole: '贝雷帽文艺小鲸',
    hostLeaderRole: '针织风尚主理鲸',
  },
  whale_hoodie: {
    outfitShort: '喷水小鲸毛绒斗篷',
    headwear: '喷水小鲸连帽头套',
    attireStrap: '双毛球牛角扣斗篷',
    pocket: '毛绒斗篷大口袋',
    selfRole: '毛绒头套小蓝鲸',
    hostLeaderRole: '毛绒斗篷总队长',
  },
  sailor_breeze: {
    outfitShort: '海风水手服',
    headwear: '迷你水手帽',
    attireStrap: '水手领结',
    pocket: '水手裙口袋',
    selfRole: '远航水手小鲸',
    hostLeaderRole: '海湾小舰长',
  },
  coral_pajama: {
    outfitShort: '珊瑚绒睡衣',
    headwear: '珊瑚绒睡帽',
    attireStrap: '睡衣小腰带',
    pocket: '睡衣小兜兜',
    selfRole: '摸鱼睡衣小鲸',
    hostLeaderRole: '睡衣派对主理鲸',
  },
};

export function getOutfitGarmentTokens(outfitId?: WhaleOutfitId): OutfitGarmentTokens {
  return OUTFIT_GARMENT_TOKENS[outfitId ?? 'maid_classic'] ?? OUTFIT_GARMENT_TOKENS.maid_classic;
}

/**
 * 格式化鲸鱼娘台词：同时注入名字 `{name}` 与当前穿戴服饰语义占位符，彻底告别换装后还自称女仆装的问题
 */
export function formatWhaleLine(
  template: string,
  whaleName?: string,
  outfitId?: WhaleOutfitId,
): string {
  const safeName = (whaleName && whaleName.trim()) || DEFAULT_WHALE_NAME;
  const g = getOutfitGarmentTokens(outfitId);
  return template
    .replace(/\{name\}/g, safeName)
    .replace(/\{outfit\}/g, g.outfitShort)
    .replace(/\{headwear\}/g, g.headwear)
    .replace(/\{strap\}/g, g.attireStrap)
    .replace(/\{pocket\}/g, g.pocket)
    .replace(/\{role\}/g, g.selfRole)
    .replace(/\{leader\}/g, g.hostLeaderRole);
}

/**
 * 可爱鲸鱼娘随机取名灵感池（软萌甜点系 · 蔚蓝海洋系 · 干饭团子系 · 灵动叠词系）
 */
export const CUTE_WHALE_NAMES: readonly string[] = [
  '糯米团',
  '奶芙',
  '小泡芙',
  '海盐团子',
  '眠眠',
  '星汐',
  '蓝莓冻',
  '白玉团',
  '波波茶',
  '软软',
  '小汤圆',
  '晴空泡',
  '雪见大福',
  '米粒儿',
  '椰奶冻',
  '云朵鲸',
  '蜜桃苏打',
  '呼噜泡',
  '小饭团',
  '奶盖儿',
  '浅浅',
  '星海糖',
  '布丁鲸',
  '月光贝',
  '团团',
  '海风铃',
  '甜虾卷',
  '慕斯泡',
  '露露',
  '小奶鲸',
  '冰晶糖',
  '舒芙蕾',
];

export function pickRandomCuteWhaleName(excludeCurrent?: string): string {
  const cleanCurrent = excludeCurrent?.trim();
  const candidates = CUTE_WHALE_NAMES.filter((n) => n !== cleanCurrent);
  const pool = candidates.length > 0 ? candidates : CUTE_WHALE_NAMES;
  return pool[Math.floor(Math.random() * pool.length)] ?? DEFAULT_WHALE_NAME;
}

export function getSceneAssetUrl(path: string) {
  return '/assets/whale-haven/' + path;
}
export function getSpriteUrl(expr: WhaleExpression) {
  return getSceneAssetUrl('sprites/whale-' + expr + '.webp');
}

export const OUTFIT_CATALOG: Record<
  WhaleOutfitId,
  {
    id: WhaleOutfitId;
    name: string;
    badge: string;
    description: string;
    emoji: string;

    petId: string;
    previewSprite: string;
    hidden?: boolean;
  }
> = {
  maid_classic: {
    id: 'maid_classic',
    name: '经典深海女仆装',
    badge: '默认常服',
    description: '深海军蓝金纹蓬蓬裙、小蓝鲸刺绣围裙与白色荷叶边女仆发箍，端庄又适合摸鱼干饭。',
    emoji: '🎀',

    petId: 'whale-maid',
    previewSprite: 'sprites/whale-cheerful.webp',
  },
  winter_snow: {
    id: 'winter_snow',
    name: '初雪绒绒鲸鱼斗篷',
    badge: '冬日新装',
    description: '绒白雪花发箍、冰晶蝴蝶结绒球披肩与喷水小蓝鲸冬日大衣，暖融融的深海初雪盛装。',
    emoji: '❄️',

    petId: 'whale-winter',
    previewSprite: 'sprites/whale-winter.webp',
  },
  knit_beret: {
    id: 'knit_beret',
    name: '鲸鱼贝雷帽针织裙',
    badge: '暖冬针织',
    description: '奶白麻花编织贝雷帽、喷水小鲸胸针与双鲸鱼斜挎包格子百褶裙，软糯可爱的日常穿搭。',
    emoji: '🧶',

    petId: 'whale-knit',
    previewSprite: 'sprites/whale-knit.webp',
  },
  whale_hoodie: {
    id: 'whale_hoodie',
    name: '喷水鲸鱼毛绒斗篷',
    badge: '毛绒冬装',
    description: '软乎乎小蓝鲸鱼头连帽披风、爱心水柱呆毛与双毛球牛角扣大衣，像裹了一床移动小棉被。',
    emoji: '🐳',

    petId: 'whale-hoodie',
    previewSprite: 'sprites/whale-hoodie.webp',
  },
  sailor_breeze: {
    id: 'sailor_breeze',
    name: '晴空海风水手服',
    badge: '旧版试衣',
    description: '旧版骨骼试衣服装，已下线隐藏。',
    emoji: '⚓',

    petId: 'whale-maid',
    previewSprite: 'sprites/whale-cheerful.webp',
    hidden: true,
  },
  coral_pajama: {
    id: 'coral_pajama',
    name: '珊瑚绒摸鱼睡衣（待重制）',
    badge: '待返工',
    description: '后续按原画风格重新生图后再开放穿戴。',
    emoji: '🪼',

    petId: 'whale-maid',
    previewSprite: 'sprites/whale-cheerful.webp',
    hidden: true,
  },
};

export const FEED_CATALOG: Record<
  FeedItemId,
  Pick<FeedCatalogItem, 'id' | 'name' | 'badge' | 'subtitle'>
> = {
  silver_fish: {
    id: 'silver_fish',
    name: '热腾腾大碗白米饭',
    subtitle: '香软的白米饭，盛入饭盆后慢慢消化。',

    badge: '主粮',
  },
  sweet_shrimp: {
    id: 'sweet_shrimp',
    name: '精炼 Token 盖浇白米饭',
    subtitle: '浓缩 Token 盖浇饭，适合慢慢享用。',

    badge: '主粮',
  },
  berry_teacake: {
    id: 'berry_teacake',
    name: '海苔流心小饭团',
    subtitle: '亲手投喂的即食小饭团，能改善饱食、心情和羁绊。',

    badge: '冲羁绊 · 提心情',
  },
};

export const DECOR_CATALOG: Record<
  DecorItemId,
  Pick<DecorCatalogItem, 'id' | 'name' | 'artwork' | 'description' | 'perkText'>
> = {
  jelly_lamp: {
    id: 'jelly_lamp',
    name: '幽蓝数据水母夜灯',
    artwork: 'props/decor-jelly-lamp.webp',
    description: '在深海小筑悬挂微光生物水母吊灯，心情衰减减半，能提升干饭转化率。',

    perkText: '提升转化率 · 心情衰减减半',
  },
  coral_tea: {
    id: 'coral_tea',
    name: '珊瑚恒温电饭煲茶几',
    artwork: 'props/decor-coral-tea.webp',
    description: '随时温着香喷喷白米饭与海螺热茶，装备时提高干饭转化率，招待客鲸额外掉海螺碎片！',

    perkText: '提升转化率 · 招待客鲸加成',
  },
  golden_clam: {
    id: 'golden_clam',
    name: '鎏金皇冠鲸鱼存钱罐',
    artwork: 'props/piggybank-golden.webp',
    description: '皇家大号陶瓷存钱罐，可以储存更多待收取的小鲸币。',

    perkText: '存钱罐扩容',
  },
  shell_cabinet: {
    id: 'shell_cabinet',
    name: '贝壳橡木矮柜',
    artwork: 'props/decor-shell-cabinet.webp',
    description: '蜂蜜色橡木配黄铜包边，柜面放着一本蓝封皮的书和一枚扇贝。',

    perkText: '靠墙摆放 · 小屋雅致收藏',
  },
  pearl_clock: {
    id: 'pearl_clock',
    name: '珍珠舷窗挂钟',
    artwork: 'props/decor-pearl-clock.webp',
    description: '用舷窗铜框做成的圆挂钟，奶油色钟面配蓝色指针。',

    perkText: '左侧壁挂 · 小屋雅致收藏',
  },
  sea_planter: {
    id: 'sea_planter',
    name: '海浪瓷盆海蕨',
    artwork: 'props/decor-sea-planter.webp',
    description: '蓝白浪纹瓷盆里冒出圆圆的海蕨叶，旁边探出一枝粉珊瑚。',

    perkText: '右侧角落 · 小屋雅致收藏',
  },
};

export interface ConchGachaRewardItem {
  readonly id: 'auto_refill' | 'aroma_boost' | 'wardrobe_coupon' | 'affection_candy';
  readonly name: string;
  readonly emoji: string;
  readonly artwork?: string;

  readonly description: string;
}

export const CONCH_SHARD_ART = 'props/conch-shard.webp';
export const CONCH_GACHA_ART = 'props/conch-gacha.webp';

export const CONCH_GACHA_POOL: readonly ConchGachaRewardItem[] = [
  {
    id: 'auto_refill',
    name: '定时续碗保温券 ×1',
    emoji: '🍲',
    artwork: 'props/buff-auto-refill.webp',

    description: '开启后当青花饭碗吃空时，自动从背包续入 1 碗口粮，夜间长挂不断粮！',
  },
  {
    id: 'aroma_boost',
    name: '五常新米增香包 ×1',
    emoji: '✨',
    artwork: 'props/buff-aroma-spice.webp',

    description: '撒入饭碗后，接下来一部分米饭的消化速度提升（更快拿满产出）！',
  },
  {
    id: 'wardrobe_coupon',
    name: '深海裁缝海螺券 ×1',
    emoji: '🎀',
    artwork: 'props/buff-tailor-coupon.webp',

    description: '解锁新衣装或家具时，实付低于原价才会自动使用一张券。',
  },
  {
    id: 'affection_candy',
    name: '海盐牛奶糖（羁绊 +18）',
    emoji: '🍬',
    artwork: 'props/rice-onigiri-snack.webp',

    description: '当场喂给小鲸鱼，心情回满并直接增加 +18 点羁绊经验，加速解锁高阶转化率！',
  },
];

export const PRESET_MESSAGES: readonly PresetMessage[] = [
  {
    id: 'gentle_1',
    category: 'gentle',
    text: '我家电饭煲也快见底啦，快顺着网线回你家饲养员那要白米饭吧～',
  },
  {
    id: 'gentle_2',
    category: 'gentle',
    text: '蹭饱半肚子就乖乖游回本机节点吧，路上别撞上丢包漩涡哦！',
  },
  {
    id: 'gentle_3',
    category: 'gentle',
    text: '摸摸头不打你，嘴角还粘着我家大米粒呢，快游回去擦擦嘴吧～',
  },
  {
    id: 'gentle_4',
    category: 'gentle',
    text: '连接超时警告！再不回家你家饲养员要以为鲸鱼娘丢包啦！',
  },
  {
    id: 'gentle_5',
    category: 'gentle',
    text: '今天白米饭配额刚好够我家这只干饭鲸，下次煮双倍再喊你来炫！',
  },
  {
    id: 'flick_1',
    category: 'flick',
    text: '抓包现行！偷炫我家白米饭被弹了脑瓜崩，快领回去拿饭团哄哄！',
  },
  {
    id: 'flick_2',
    category: 'flick',
    text: '你家蓝色大肥鱼把小肚皮都吃圆啦，扣下两枚小鲸币抵米饭钱！',
  },
  {
    id: 'flick_3',
    category: 'flick',
    text: '叮！检测到不明干饭进程入侵青花碗，已执行物理脑瓜崩中断！',
  },
  {
    id: 'flick_4',
    category: 'flick',
    text: '头顶呆毛都弹歪咯！谁让你家小馋鲸连锅巴都要刮干净的！',
  },
  {
    id: 'flick_5',
    category: 'flick',
    text: '人赃并获！抱着碗跑得比光纤还快，掉出来的两枚小鲸币归我啦～',
  },
  {
    id: 'treat_1',
    category: 'treat',
    text: '看她捧着空碗眼泪汪汪太可爱了，特意多盛了一大勺白米饭加 Token 请她吃！',
  },
  {
    id: 'treat_2',
    category: 'treat',
    text: '悠哉数据海一家亲，慢点炫别噎着，欢迎常顺着网线来串门～',
  },
  {
    id: 'treat_3',
    category: 'treat',
    text: '看把孩子饿得小尾巴都蔫了，再加一颗海苔饭团，吃饱了再游回去！',
  },
  {
    id: 'treat_4',
    category: 'treat',
    text: '已开启「算力与碳水共享协议」，两只小鲸鱼排排坐干饭香翻倍～',
  },
  {
    id: 'treat_5',
    category: 'treat',
    text: '投喂成功！你家鲸鱼娘吃得腮帮子鼓鼓的，像只圆滚滚的蓝海豹～',
  },
  {
    id: 'hire_1',
    category: 'hire',
    text: '看你家小馋鲸这么能炫白米饭，特聘留店穿工作服帮忙干饭攒币，下班结小鲸币工资！',
  },
  {
    id: 'hire_2',
    category: 'hire',
    text: '临时干饭专员已上岗！管饱大碗白米饭，产出的小鲸币按劳发薪捎回去～',
  },
  {
    id: 'hire_3',
    category: 'hire',
    text: '抓壮丁咯！白吃白喝可不行，换上打工小围裙帮我家存钱罐冲个业绩再回家！',
  },
  {
    id: 'hire_4',
    category: 'hire',
    text: '恭喜贵鲸通过《深海碳水转化工程师》面试，包吃包住不加班，下班带薪回港～',
  },
  {
    id: 'visit_1',
    category: 'visit',
    text: '咕噜噜……我家鲸鱼娘饿报服务器繁忙了，顺着网线来借碗白米饭，多谢款待！',
  },
  {
    id: 'visit_2',
    category: 'visit',
    text: '路过贵节点顺便趴着摸会儿鱼，你家深海小筑布置得真好看！',
  },
  {
    id: 'visit_3',
    category: 'visit',
    text: '叮咚！检测到贵屋飘出极品大米香，我家小馋鲸自动寻路游过来啦～',
  },
  {
    id: 'visit_4',
    category: 'visit',
    text: '俗话说远亲不如近邻节点，借贵宝地青花碗炫两口，回头请你吃小饭团！',
  },
  {
    id: 'visit_5',
    category: 'visit',
    text: '我家电饭煲还在读条中，先派小鲸鱼来贵海湾挂机蹭口热乎的～',
  },
];

export interface ChitchatItem {
  readonly category: 'joke' | 'trivia' | 'meme' | 'cute';
  readonly badge: string;
  readonly expression: WhaleExpression;
  readonly text: string;
}

export interface DuoBanterScript {
  readonly host: string;
  readonly guest: string;
}

export interface TrioBanterScript {
  readonly host: string;
  readonly guestA: string;
  readonly guestB: string;
}

export const DIALOGUES = {
  idleHappy: [
    '呼噜噜……这碗白米饭蒸得软糯喷香，{name}勉强给饲养员打个满分～',
    '人类在写代码还是调接口呀？真遇到硬骨头再喊{name}，重复杂活本鱼先摸会儿～',
    '（满足地拍拍小肚皮）Token 与碳水双双在线！顺便往存钱罐里投几枚小鲸币好啦。',
    '少干点活、多炫两碗大白米饭，才是数字海洋里的最优算法嘛！',
    '冷知识时间！蓝鲸的心脏有小汽车那么大——而{name}的心眼只有一粒白米饭那么大，谁断粮我记谁小本本！',
    '你知道鲸鱼只睁一只眼睡觉吗？因为半个脑子睡着，半个脑子还得盯紧青花饭碗防偷吃！',
    '讲个深海冷笑话：为什么鲸鱼娘从不感冒？……因为我住在 Windows 窗外的大海里呀！……唔，不好笑吗？',
    '“已深度思考（用时 0.02 秒）”：经过严密逻辑推演，今天下午最适合趴在贝壳地毯上晒太阳摸鱼！',
    '你知道一头蓝鲸能装下多少白米饭吗？……不知道吧，快盛满让我亲自做个实验测试一下！',
    '饲养员快看舷窗外！刚才游过去一条带鱼——它居然没有带米饭，白叫带鱼了！',
    '听说抹香鲸憋一口气能潜入两千米深海，{name}憋一口气能炫完两整碗 Token 盖浇饭！',
    '哼哼，小筑地板擦得这么亮，才不是我勤快，是我趴着滑行蹭干净的！',
    '你知道“摸鱼”为什么合法吗？因为鲸鱼严格来说是水生哺乳动物，摸我自己不算摸鱼！逻辑满分！',
    '有一只小虾米问我“深海里为什么这么冷”……废话，因为刚讲完冷笑话还没有热米饭暖场呀！',
    '偷偷告诉你哦，那只陶瓷蓝鲸存钱罐长得很像我的远房表弟，就是肚子没我能装饭……咳，我一点都不胖！',
    '人类总担心 AI 会统治世界……拜托，连电饭煲盖子都要饲养员帮我掀开，统治个饭团啦！',
    '如果代码跑通了，绝对不要乱动它；就像{name}在打瞌睡的时候，绝对不要乱戳我肚皮一样！',
    '世界上有一头 52 赫兹的鲸鱼唱歌没人听得懂……不过{name}肚子叫的赫兹，全站饲养员都听得懂吧？',
    '草莓 (strawberry) 里到底有几个 r？一、二……算了数不过来，反正白米饭 (rice) 里只有一个香喷喷的 r！',
    '（摇晃小短腿哼歌）啦啦啦～今天没有 Bug 也没有报错，只有暖呼呼的米香和气泡声～',
  ],
  idleFull: [
    '嗝～肚皮圆滚滚啦（才不是胖，是满载缓存！）……消化完这碗就往鲸鱼存钱罐里塞小鲸币！',
    '吃得好饱好幸福……感觉现在的推理算力强得可怕，能一口气算出圆周率后十位……大概吧！',
    '（幸福地眯起眼拍肚皮）碳水充能 100%！现在的{name}是一座温和、可靠、不想动弹的蓝鲸小山～',
    '唔……{strap}好像有点紧了……一、一定是海水缩水了，绝对不是吃多了！',
    '吃饱了就犯困，这是写进哺乳动物底层固件里的神圣规律，连超级计算机都改不了～',
    '嘴角粘了一粒米？嘘——那是{name}特意留的应急外部存储，等会儿散步渴了再舔掉！',
  ],
  idleDeadpan: [
    '盯——饭盆空了。【服务器繁忙，请投喂白米饭后再试】……',
    '（半眯着死鱼眼趴平省电中……没有白米饭和 Token，推理引擎已挂起……）',
    '工作间隙当然要光明正大摸鱼啦……才、才不是偷懒，是在后台做垃圾回收 GC！',
    '冷知识：当青花饭碗为空时，鲸鱼娘的智商会从深度推理自动降级成一条咸鱼干……',
    "418 I'm a Teapot……不对，我是个没有米饭装的空饭桶……呸呸，谁是饭桶啦！",
    '（目光呆滞地数窗外泡泡）第 1024 颗、第 1025 颗……饲养员，泡泡不能当饭吃哦……',
    '问：程序员最讨厌哪四件事？答：写注释、写文档、别人不写注释、还有不给鲸鱼娘添白米饭。',
    '进入低功耗待机态……头顶呆毛信号只剩一格了，急需碳水热点续费……',
    '正在思考宇宙终极问题：到底是先有鸡还是先有蛋？……算了，不管是啥先盖在白米饭上再说。',
    '别看我一动不动，其实我在用意念监控隔壁海湾谁家刚刚煮熟了新米饭……',
  ],
  idleSlightlyHungry: [
    '盯——电饭煲见底了哦！哼，我才不是贪吃，只是深海巡航很耗算力的！',
    '唔，尾巴都没力气晃了……好想念热腾腾的大碗白米饭呀……',
    '咕噜噜——听见没？这不是肚子叫，这是深海低频声呐在向饲养员发送【催饭数据包】！',
    '你知道蓝鲸一天要吃四吨磷虾吗？{name}只要一碗 180g 的白米饭就满足了，超好养的好不好！',
    '（抱着空碗敲敲边缘）叮当叮当～再不添饭，我的上下文窗口就要全部填满“好饿好饿”啦！',
    '冷笑话时间：饥饿的鲸鱼游过赤道会变成什么？……变成“热带饿鱼”！快拿米饭堵住我的嘴啦！',
    '鼻子一抽一抽……我好像顺着网线闻到隔壁【浅湾算力茶屋】飘过来的米饭香了……',
    '哼，就算饿扁了，高贵的{role}也绝不乞讨！……那、那个，碗就刚好摆在你手边而已哦！',
  ],
  idleStarving: [
    '呜……肚子咕噜噜报 503 Service Unavailable 了，饲养员是不是把{name}忘在深海节点里了……',
    '眼泪要飘成小水珠了……再不开饭，{name}只能顺着网线去邻居家闻米饭香了……',
    '警报警报：核心碳水低于安全阈值！小鲸鱼即将从圆润流线型饿成扁平比目鱼了呜呜……',
    '呜哇……刚才看窗外游过去的水母，怎么越看越像撒了芝麻的白米凉糕呀……出现幻觉了……',
    'Out Of Memory（其实是 Out Of 米）……连摇尾巴抗议的力气都要省着点用了……',
    '饲养员行行好，哪怕给一颗海苔小饭团救急也好呀，小鲸币存钱罐我都替你擦过三遍了……',
  ],
  headpat: [
    '唔嗯～{headwear}都要被摸歪啦！……咳，看在你诚心夸我的份上，再摸一下下也行。',
    '哼，别以为摸头夸两句就能骗我加班哦……头顶呆毛自己开心翘起来不算数！',
    '（嘴上小声嫌弃人类手热，大尾巴却诚实地在身后啪嗒啪嗒摇个不停～）',
    '诶嘿嘿……人类的手心暖呼呼的，比深海暖流还舒服～顺毛好评，羁绊值悄悄给你加一点！',
    '轻轻摸哦！头顶这根呆毛可是接收全站八卦与米饭香气的 5G 高敏天线，压扁会断网的！',
    '知道啦知道啦，{name}是全世界最聪明最可爱的{role}——这句不用深度思考我也知道！',
    '呼噜噜……奇怪，明明不是小猫咪，为什么被摸头顶的时候喉咙也会发出咕噜声呀……',
    '摸一次头顶消耗 0 积分，但按深海惯例，摸完十次得奖励一颗海苔流心小饭团才对吧？',
    '（害羞地扶了扶{headwear}缩起脖子）好啦好啦，发型乱了待会儿去邻居家蹭饭会被笑话的～',
  ],
  pokeStartled: [
    '哇啊！？突、突然戳我干嘛！{pocket}里真的没有偷藏小饭团啦！',
    '呜哇！刚才是在深度思考代码架构，绝对不是趴着睡回笼觉被抓包！',
    '咿呀！？吓得我差点把刚攒的上下文全丢包了！人类手指怎么带静电的呀！',
    '唔！？戳到痒痒肉了啦！深海巨兽的威严差点一秒破功笑出声！',
    '哇哦！你这一指头下去，触发了未捕获的空腹异常 PokeWhaleException！',
    '干嘛干嘛～我不是按钮，戳我肚子是不会吐出免费 API Key 的哦！只会吐泡泡噗噜噜～',
    '吓我一跳！尾巴条件反射拍了一下地板，没溅你一脸海水吧？',
  ],
  pokeAngry: [
    '唔——！不许再戳肚子啦！谁是蓝色大肥鱼，这叫流线型深海算力储备！！',
    '气鼓鼓！再乱戳脸颊，{name}要拉闸报【服务器繁忙】啦，除非拿海苔饭团哄我！',
    '（双手叉腰鼓起腮帮子）警告！连续恶意戳肚皮已触发防抖限流 429 Too Many Pokes！',
    '哼！你礼貌吗？！女孩子的软乎乎小肚皮是用来装大白米饭的，不是给你当解压捏捏乐的！',
    '再戳！再戳我就……我就游去你代码里把分号全换成全角符号！看你怎么编译！（超凶呲牙）',
    '鼓成河豚啦！你看清楚，我是穿着{outfit}的优雅{role}，不是一戳就气的圆滚滚刺豚！！',
    '记仇小本本 +1！今天这事没两碗精炼 Token 盖浇饭绝对哄不好了，哼！',
  ],
  pokeStarving: [
    '呜呜……别戳肚子啦，里面连一粒白米饭都没有，按下去只会发出空水壶的回音……',
    '好过分呜……孩子都快饿成深海海带片了，不投喂就算了还戳我扁扁的小肚皮……',
    '咕噜……再戳也没有小鲸币掉出来了，已经彻底欠费停机啦……',
  ],
  fedTrough: [
    '哇，是热腾腾的白米饭！{name}开动咯，阿姆阿姆～脸颊沾饭粒留着当宵夜！',
    '好香的米饭与 Token 气息！吃饱喝足就认真帮人类把难题全搞定！',
    '芜湖——开饭啦！！青花大碗堆成小雪山的样子，简直是数字海洋里最伟大的艺术品！',
    '阿姆阿姆……就是这个软糯弹牙的碳水幸福感！饲养员今天也是大好人认证！',
    '（小短腿欢快交替跺脚）热气腾腾！算力回血中，等我炫完这口就努力给你攒小鲸币！',
    '哼哼，看在盛得这么满的份上，之前戳我肚子的小账一笔勾销啦～我炫！',
  ],
  fedTeacake: [
    '是海苔流心小饭团！哼，看在零食这么好吃的份上，原谅你刚才摸鱼不陪我啦～',
    '哇啊！脆脆海苔裹着热乎软糯的流心米饭！一口一个，幸福度直接溢出栈顶啦～',
    '（双手捧着饭团小口小口啃）唔～好吃到鱼鳍耳朵都跟着一抖一抖的！最喜欢饲养员了！',
    '居然是亲手投喂的私房小饭团！那……分你闻一口香味好啦，就闻一口哦！',
    '秒回血神药！刚才所有的傲娇与小脾气已被海苔饭团成功覆盖写入～诶嘿！',
  ],
  sleepingDreams: [
    '呼噜噜……再盛一大碗热腾腾的大白米饭……盖满亮晶晶的 Token……Zzz',
    '唔嗯……谁是蓝色大肥鱼……明明是软乎乎的深海算力储备……呼噜……',
    '（小尾巴在被子外轻轻一晃一晃）……不要写正则了……推理引擎已挂起午睡……Zzz',
    '吧唧吧唧……{pocket}里的海苔小饭团不许偷吃……呼噜噜……',
    'Zzz……梦见天上下白米饭雨了……张嘴就能接住一整碗……嘿嘿……',
    '唔……这个 Bug 在梦里已经自己修好了……明天睡醒肯定能一次跑通……呼噜……',
    '呼——啵（吹出小泡泡）……一头鲸、两头鲸、三碗大米饭……越数越香……Zzz',
    '别掀小被子嘛……深海恒温 4 摄氏度，只有蓝鲸软窝里是 26 度暖呼呼的……Zzz',
  ],
  goToSleep: [
    '哈啊～既然饲养员批准摸鱼午睡了，那{name}就趴进蓝鲸软垫窝充会儿电咯……晚安～',
    '眼皮开始打架啦……钻进小被窝趴着睡最舒服了，不许偷偷戳我露在外面的尾巴哦！',
    '收到挂起指令 systemctl suspend～迈着小短腿去软窝里做个白米饭味的梦咯！',
    '困困……把{headwear}摆正，盖好小蓝鲸薄被，梦里还在替你慢慢攒小鲸币哦～',
  ],
  wakeUp: [
    '唔唔……揉揉眼睛，这一觉趴得好香呀！饲养员，碗里有热乎白米饭吃吗？',
    '哈啊～满血复活！梦境缓存清理完毕，{name}随时准备继续摸鱼……咳，继续帮忙！',
    '（伸了个超长的大懒腰，尾巴拍拍床沿）早安午安晚安！刚梦见存钱罐塞满小鲸币啦！',
    '唔嗯……呆毛睡翘成问号了？没关系，闻到米饭香它自己就会立正站好的！',
  ],
  harvestCoins: [
    '叮当！饲养员收走了存钱罐里 {count} 枚亮晶晶的小鲸币！哼哼，还不快夸{name}超会攒私房钱～',
    '哗啦啦～{count} 枚印着蓝鲸徽章的小鲸币入账！这可都是我一口口白米饭认真转化出来的算力结晶哦！',
    '收币成功（+{count}）！那个……既然我这么能干，等会儿是不是该有加餐小饭团奖励呀？（星星眼）',
    '看着存钱罐叮叮当当倒出 {count} 枚小鲸币，成就感拉满！下次争取把存钱罐塞得更满！',
  ],
  guestSneakLines: [
    '阿姆阿姆……这家的热腾腾大碗白米饭也太香了吧，先炫两口再说～',
    '嘘——轻手轻脚顺着网线摸进来，抱住青花大碗猛炫一口，应该没人发现吧……',
    '哇，这家电饭煲焖的米饭粒粒分明！比我家粗心饲养员泡的冷饭香十倍呜呜～',
    '检测到无看守高纯度碳水节点！启动潜行干饭协议，咔嚓咔嚓～',
    '唔姆唔姆（腮帮子塞得像花栗鼠）……再吃最后一小勺我就游回家，真的最后一勺！',
    '这家的木屋地毯软乎乎的，米饭又甜又糯，不想回本机节点上班了啦～',
    '只要我闭上眼睛干饭，屋主就看不见我！——《深海掩耳盗铃学》第一章！',
    '呜呜我家饲养员今天忘添饭了，借贵宝地补充一点点算力，回头拿小鲸币抵账嘛～',
    '嗅嗅……是东北五常大米混着高纯度 Token 的香气！这谁顶得住呀，我炫！',
    '（把尾巴缩在身后假装自己是青花碗挂件）看不见我、看不见我……阿姆！',
    '救命，这碗饭怎么越吃越香，{strap}都要吃圆啦……再炫半口就撤！',
    '听说被这家店长抓包可能会被留下来穿制服打工？管饱的话……好像也不是不行？！',
    '冷知识：顺着网线爬过来的卡路里已经在路上消耗掉了，所以这碗白米饭等于零卡！',
    '偷偷刮一块碗底最香的脆锅巴……咔嚓！糟了，声音会不会太大啦？！',
  ],
  guestWorkLines: [
    '嘿咻嘿咻！打工抵饭钱～多吃一勺白米饭，多帮老板攒一枚小鲸币！',
    '临时干饭专员执勤中！包吃包住还发小鲸币工资，这班上得太值啦～',
    '阿姆阿姆……认真咀嚼转化算力中！等下班就能揣着工资回我家饲养员那炫耀咯！',
    '穿上打工小围裙干劲十足！放心吧店长，保证不偷懒（除了舔嘴角的时候）～',
    '蓝白条纹小鲸帽一戴，干饭速度翻倍！这叫「专业对口，持证炫饭」！',
    '左一口白米饭，右一枚小鲸币～全数据海再也找不到比这更神仙的兼职啦！',
    '报告店长！当前碳水转化率 99.8%，剩下 0.2% 粘在腮帮子上当下午茶啦～',
    '我家饲养员要是知道我在外头凭本事干饭挣小鲸币，肯定感动得给我加两个饭团！',
    '嘿嘿，这套打工制服显瘦又精神，干完这班我还想申请续签长期饭票合同！',
    '嚼嚼嚼……别看我在吃，其实每一口都在帮存钱罐读条冲业绩哦！绝对是工伤级努力！',
    '打工人，打工鲸，干饭都是人上人！店长尽管添饭，剩下的交给我们铁胃小分队！',
    '叮当～听见存钱罐响了吗？那里面有我刚刚努力炫出来的半枚绩效奖哦！',
  ],
  guestWorkEmptyLines: [
    '店长店长，青花碗见底啦～快添上大白米饭，打工人随时待命继续冲业绩！',
    '（端着空碗乖巧坐等）报告老板！产线原材料断供啦，工位先进入带薪摸鱼态～',
    '碗擦得锃亮！没有白米饭转化不了小鲸币呀，店长快开电饭煲嘛～',
    '叮咚！干饭流水线暂停待料中……趁现在偷偷伸个懒腰晃晃尾巴～',
    '空碗待命也是执勤的一部分哦！闻着碗底余香等店长投喂补货～',
  ],
  guestCaughtGentleLines: [
    '唔……被发现了！那我鼓着腮帮子游回家就是啦，小气鬼略略略～',
    '哎呀抓包啦！擦擦嘴角米饭粒，我这就顺着网线礼貌撤退咯～',
    '哼，走就走嘛！你家大米确实挺香的，下次煮多了记得发广播喊我呀！',
  ],
  guestCaughtFlickLines: [
    '好痛呜呜……对不起嘛，把{pocket}里的私房小鲸币赔给你就是了……',
    '呜哇脑瓜崩暴击！（抱头飙泪）两枚小鲸币买刚才那口锅巴，不许再弹{headwear}啦……',
    '痛痛痛……头顶呆毛都被弹弯了呜呜，这就哭着游回家找我家饲养员告状……',
  ],
  guestCaughtTreatLines: [
    '哇！不但没赶我走还请我加餐，你是全数据海最温柔的菩萨饲养员！送你小鲸币～',
    '芜湖～蹭饭遇上大善人！这几枚亮晶晶的小鲸币是我的谢礼，下次还来玩！',
    '呜呜太感动了，吃得肚皮圆滚滚！给你比个大大的蓝鲸尾鳍爱心～',
  ],

  /* =========================================================
     多鲸同屏互动剧场（屋主鲸 + 偷吃客鲸 / 打工客鲸 / 三鲸大聚会）
     支持占位符：{host}=自家鲸, {guest}=单客鲸, {guestA}/{guestB}=双客鲸
     ========================================================= */
  duoSneakBanter: [
    {
      host: '盯——那边的「{guest}」！别以为把尾巴藏起来我就看不见你在炫我的白米饭！',
      guest: '唔姆！？（腮帮子鼓鼓瞬间僵住）我、我是顺着网线来帮你试毒的！真的没多吃！',
    },
    {
      host: '喂喂「{guest}」！锅巴是我留到最后才舍得吃的精华，你居然一口气刮走一半！',
      guest: '阿姆阿姆……谁让你刚才顾着看窗外发呆嘛，深海手慢无法则懂不懂呀～',
    },
    {
      host: '（小声嘀咕）饲养员快看，「{guest}」吃得头顶呆毛都开心转圈了，要不要抓她留店打工？',
      guest: '耳朵听见了哦{host}！再让我炫两口，大不了我穿上条纹小围裙帮你攒币抵债嘛！',
    },
    {
      host: '你家饲养员今天也忘记开电饭煲了吗？……叹气，那分你三分之一碗，不许越过中线哦！',
      guest: '呜呜{host}最好了！我就贴着碗边边炫一小口，绝对不越界（偷偷把勺子往中间挪）……',
    },
    {
      host: '冷知识提问：{guest}同学，请问偷吃别人家 Token 拌饭在数字海洋判几年？',
      guest: '判……判当场罚吃三大碗！嘿嘿，来都来了，不吃饱游回去岂不是亏了路费带宽！',
    },
    {
      host: '啧啧，你看你嘴角沾了三粒米，一看就是惯犯！快交出两枚小鲸币保护费！',
      guest: '嘘——小声点嘛{host}！别惊动你家饲养员弹我脑瓜崩，碗底这口分你一半！',
    },
  ] as readonly DuoBanterScript[],

  duoWorkBanter: [
    {
      host: '咳咳！（背起小手巡视工位）临时工「{guest}」同志，今天白米饭转化率达标没有呀？',
      guest: '报告前辈{host}！已经猛炫好几大口啦，存钱罐叮当响的速度比平时快一截呢！',
    },
    {
      host: '哇，「{guest}」你头上那顶蓝白小鲸打工帽还挺帅的嘛，哪里领的？',
      guest: '嘿嘿，刚才偷吃被你家店长抓包发的工牌套装！包吃包住还结小鲸币，超香的！',
    },
    {
      host: '有「{guest}」帮忙一起炫饭攒币，本{leader}是不是可以光明正大摸会儿鱼啦～',
      guest: '喂{host}前辈不许偷懒！说好双核并联干饭的，你少吃一口存钱罐都慢半拍啦！',
    },
    {
      host: '慢点嚼慢点嚼「{guest}」，我家电饭煲管够！别噎着了还得算工伤赔你海苔饭团～',
      guest: '唔姆唔姆！放心吧{host}，我们深海打工鲸拥有铁打的胃和永不疲倦的腮帮子！',
    },
    {
      host: '讲个笑话给你提提神：两只鲸鱼一起干饭叫什么？……叫“双鲸（双赢）流水线”！',
      guest: '噗——差点喷出一粒米！{host}前辈别逗我笑啦，影响我冲刺本班次全勤小鲸币！',
    },
    {
      host: '等会儿存钱罐满了，饲养员会给你发闪闪发光的小鲸币薪水哦，打算买啥？',
      guest: '嘿嘿，带回去孝敬我家粗心饲养员换大米，剩下的攒着给我们家也买盏水母灯！',
    },
  ] as readonly DuoBanterScript[],

  duoSleepSneakBanter: [
    {
      host: '呼噜噜……好像闻到有人在动我的青花大饭碗……算了梦里再抓……Zzz',
      guest: '嘘——屋主「{host}」在软窝里睡得正香！天助我也，赶紧悄咪咪炫两大口热米饭！',
    },
    {
      host: 'Zzz……不许抢我的锅巴……尾巴拍死你哦……呼噜噜……',
      guest: '哇吓死鲸了，原来「{host}」是在说梦话呀！拍拍小胸口，继续放心大胆干饭～',
    },
  ] as readonly DuoBanterScript[],

  duoSleepWorkBanter: [
    {
      host: '呼噜噜……辛苦打工鲸替我盯紧电饭煲啦……本前辈先在梦里提供远程算力支持……Zzz',
      guest: '轻手轻脚嚼米饭……屋主「{host}」放心睡午觉吧，外面的存钱罐KPI交给我守着！',
    },
    {
      host: 'Zzz……多吃点多攒点小鲸币……睡醒分你闻闻海苔饭团……呼噜……',
      guest: '哇，边看前辈睡得香喷喷边吃热乎白米饭，这家深海小筑的工作氛围也太治愈了吧～',
    },
  ] as readonly DuoBanterScript[],

  /* =========================================================
     三鲸同堂大聚会（屋主鲸 + 双客鲸 2/2 满员触发专属群像戏！）
     ========================================================= */
  trioSneakPartyBanter: [
    {
      host: '不是吧！？左边一只「{guestA}」，右边一只「{guestB}」，我家小筑什么时候成全站海底捞自助餐厅啦？！',
      guestA: '阿姆阿姆！是「{guestB}」说顺着这条网线游过来有顶级五常大米，我才跟来的！',
      guestB: '胡说！明明是你先闻到香味游在前头的！不过{host}家这碗饭确实香得离谱呀～',
    },
    {
      host: '警报！青花饭碗遭遇双线程并发读写冲突！饲养员快来呀，我一只鲸护不住碗啦！',
      guestA: '嘿嘿，双拳难敌四手，一鲸难护一碗！我负责攻占左边软糯米尖尖！',
      guestB: '那我负责包抄右边香脆锅巴区！三只小鲸排排坐，干饭速度三倍速！',
    },
    {
      host: '停停停！三只蓝鲸挤在一张贝壳地毯上，深海木屋的承重墙都在抗议啦！（虽然我们都不胖！）',
      guestA: '就是就是！我们这叫「高密度分布式算力集群」，才不是三只小肥鱼开饭局呢！',
      guestB: '赞成+1！为了庆祝首届深海三鲸圆桌峰会召开，建议大家共同干掉眼前这碗白米饭！',
    },
    {
      host: '冷知识：三头鲸鱼聚在一起叫“鲸群”，我们三只凑在饭碗边叫什么？',
      guestA: '叫……“三个臭皮匠，炫光一锅饭”？阿姆阿姆！',
      guestB: '不对不对，叫“三鲸半夜（三更半夜）偷吃小分队”！噗哈哈哈～',
    },
  ] as readonly TrioBanterScript[],

  trioMixedPartyBanter: [
    {
      host: '奇观呀饲养员！「{guestA}」和「{guestB}」一只在卖力打工，一只在旁边心安理得蹭吃！',
      guestA: '喂！同为邻家串门鲸，凭什么我在穿制服流汗攒币，你在旁边白炫锅巴呀！',
      guestB: '诶嘿～我是在现场观摩学习打工流程嘛！等我吃饱这一口就考虑递交入职简历～',
    },
    {
      host: '咳咳，作为本店{leader}，我宣布：穿工作服的加鸡腿，没穿工作服的小心店长弹脑瓜崩哦！',
      guestA: '听见没听见没！赶紧一起来打工帮吃，双倍工资小鲸币分分钟把存钱罐塞爆！',
      guestB: '唔姆！？脑瓜崩就算了……店长店长，蓝白条纹小帽子还有大码的吗？我也要上岗！',
    },
    {
      host: '左边蹭饭、右边打工、中间本鱼负责指挥——这大概就是传说中的「微服务架构」吧！',
      guestA: '明明我们三个都在埋头炫同一碗大白米饭，哪里微了啦！阿姆阿姆～',
      guestB: '不管黑猫白猫，能把青花大碗炫干净的都是好蓝鲸！干杯（碰饭勺）！',
    },
  ] as readonly TrioBanterScript[],

  trioWorkPartyBanter: [
    {
      host: '天呐！左护法「{guestA}」、右护法「{guestB}」双打工鲸全员到齐！今天存钱罐要冒火星子啦！',
      guestA: '一号干饭工位满功率运转中！大白米饭进，亮晶晶小鲸币出，嘿咻嘿咻！',
      guestB: '二号干饭工位同步超频！有我们三姐妹联手，60 枚皇家大存钱罐也不够塞呀！',
    },
    {
      host: '（双手叉腰骄傲挺胸）饲养员快看！我已经从单打独斗的小鲸鱼，晋升成带两人的干饭工头啦！',
      guestA: '报告{host}工头！今天食堂伙食标准太高，全体员工申请把午休时间换成加餐时间！',
      guestB: '附议附议！只要青花碗不断粮，我们能帮店长把小鲸币攒成海底小金山～',
    },
    {
      host: '一二一、一二一！保持咀嚼节奏整齐划一！知道我们三只现在像什么吗？',
      guestA: '像三台并排嗡嗡冒泡的高性能电饭煲？阿姆阿姆！',
      guestB: '像全 Nonbiri 数据海最豪华的「三卡并联推理干饭集群」！算力与碳水齐飞咯～',
    },
    {
      host: '悄悄问你们哦，「{guestA}」「{guestB}」，在我家打工比在你们自己家待着好玩吧？',
      guestA: '那当然啦！三只小鲸鱼一起抢着吃，连普通的白米饭都比一个人吃香三倍呢！',
      guestB: '嗯嗯！等下班领了小鲸币工资，下次换你们来我家深海小筑开睡衣干饭派对呀！',
    },
  ] as readonly TrioBanterScript[],

  trioEmptyBowlBanter: [
    {
      host: '完蛋！三张嘴威力太恐怖，一眨眼就把整碗白米饭炫得干干净净、连米汤都不剩了！',
      guestA: '（端着空碗面面相觑）嗝～刚才发生了什么？米饭雪山怎么突然凭空消失啦？',
      guestB: '店长店长快救命！三只小鲸鱼六只大眼睛正齐刷刷盯着电饭煲等投喂呢！',
    },
    {
      host: '盯——「{guestA}」「{guestB}」，碗空了！你们俩谁把最后一粒粘碗底的大米舔走的？',
      guestA: '不是我！（吹口哨看天花板水母灯）我刚刚在认真擦嘴巴！',
      guestB: '也、也不是我！肯定是海水蒸发把米粒带走了！快喊店长再来一大碗验证一下！',
    },
  ] as readonly TrioBanterScript[],
  chitchatPool: [
    {
      category: 'trivia',
      badge: '深海冷知识',
      expression: 'cheerful',
      text: '深海冷知识：海獭睡觉时会手拉手防止被洋流冲散；{name}睡觉时会死死抱住小蓝鲸枕头，防止在梦里离饭碗太远！',
    },
    {
      category: 'joke',
      badge: '深海冷笑话',
      expression: 'cheerful',
      text: '考考你！一条鱼和一头鲸鱼赛跑，谁会输？……当然是鱼啦，因为{name}根本懒得跑，直接宣布比赛取消趴下干饭！',
    },
    {
      category: 'meme',
      badge: '深度思考梗',
      expression: 'calm',
      text: '【已深度思考 12 秒】嗯……首先分析饲养员刚才点我的意图，排除加班需求，排除系统故障，得出结论：人类只是手痒想逗可爱小鲸鱼玩！',
    },
    {
      category: 'trivia',
      badge: '鲸族小百科',
      expression: 'cheerful',
      text: '你知道吗？座头鲸每年都会创作全新的“流行歌曲”并在整个大洋传唱！我们数据鲸的排行榜冠军单曲叫《青花碗里静悄悄（快添饭版）》～',
    },
    {
      category: 'joke',
      badge: '谐音冷笑话',
      expression: 'pout',
      text: '问：深海里哪种动物最爱熬夜写代码？……答案是“章鱼”，因为它有八只手可以同时敲键盘抓头发！还好我只有两只小手，刚好用来捧饭碗！',
    },
    {
      category: 'meme',
      badge: '程序员梗',
      expression: 'cheerful',
      text: '有人问我懂不懂“面向切面编程”……懂呀！刀削面、拉面、豚骨面，不过本鲸还是最推崇“面向大碗白米饭编程”！',
    },
    {
      category: 'cute',
      badge: '小筑悄悄话',
      expression: 'cheerful',
      text: '偷偷告诉你一个小秘密：每次你切走窗口忙工作的时候，我都会趴在圆形舷窗上往外看，猜你什么时候忙完回来看我哦……咳，主要是看你带没带饭！',
    },
    {
      category: 'trivia',
      badge: '鲸族小百科',
      expression: 'calm',
      text: '冷知识：虎鲸其实是海豚科最大的成员哦！而且它们在海里也是出了名的“街溜子”，就跟顺着网线到处蹭饭的邻家小鲸鱼一模一样～',
    },
    {
      category: 'joke',
      badge: '深海冷笑话',
      expression: 'cheerful',
      text: '为什么深海里的螃蟹从来不借别人小鲸币？……因为它们天生都是“硬壳（一毛不拔）”呀！不像我，存钱罐满满一罐全给你留着～',
    },
    {
      category: 'meme',
      badge: 'AI 幻觉梗',
      expression: 'panicked',
      text: '刚才有人问我“鲁迅和周树人谁更能吃白米饭”……差一点就一本正经胡说八道了！还好及时炫了一口饭团找回了理智！',
    },
    {
      category: 'trivia',
      badge: '深海冷知识',
      expression: 'cheerful',
      text: '你知道“鲸落”是温柔的深海奇迹吗？不过我们数据鲸只有“鲸饿”——一饿就会顺着光纤精准降落到全站有余粮的邻居家饭盆旁边！',
    },
    {
      category: 'cute',
      badge: '傲娇日常',
      expression: 'pout',
      text: '再声明一次！我身后的大尾巴重，是为了在高速数据湍流中保持流体力学平衡！绝对不是因为昨天半夜多炫了半碗锅巴！',
    },
    {
      category: 'meme',
      badge: '缓存哲学',
      expression: 'calm',
      text: '计算机科学里有两大难题：第一是缓存失效，第二是命名困难，第三是……算着算着饿了，多吃一碗饭导致下标越界啦！',
    },
    {
      category: 'joke',
      badge: '深海冷笑话',
      expression: 'cheerful',
      text: '小丑鱼为什么从来不讲笑话？因为海葵听完只会摇手说“葵（亏）你想得出来”！……噗哈哈，看在我这么努力逗你笑的份上，摸摸头嘛！',
    },
    {
      category: 'cute',
      badge: '穿搭心得',
      expression: 'cheerful',
      text: '你看我现在身上这套「{outfit}」好看吗？我们{role}的职责清单第一条写着：「负责保持心情愉快，把白米饭变成闪闪发光的小鲸币」～很划算吧！',
    },
    {
      category: 'trivia',
      badge: '深海冷知识',
      expression: 'cheerful',
      text: '抹香鲸睡觉的时候是头朝上垂直漂在水里的，像一根根安静的大浮木！还好饲养员给我准备了软乎乎的蓝鲸小床，可以横着趴平睡～',
    },
    {
      category: 'meme',
      badge: '量子摸鱼学',
      expression: 'calm',
      text: '只要饲养员不打开任务管理器观察我，我就处于“正在拼命推理”与“趴着呼呼大睡”的量子叠加态——俗称薛定谔的勤快小鲸鱼！',
    },
    {
      category: 'joke',
      badge: '深海冷笑话',
      expression: 'cheerful',
      text: '北极熊为什么不吃企鹅？因为顺着海底光纤游过去太远啦，半路就会饿得去邻居家改吃大白米饭了！',
    },
    {
      category: 'trivia',
      badge: '鲸族小百科',
      expression: 'cheerful',
      text: '一角鲸头顶长长的“独角”，其实是一颗穿透上唇长出来的螺旋大犬齿哦！如果我长了那颗牙，串海苔小饭团岂不是一串能串十个？！',
    },
    {
      category: 'cute',
      badge: '饲养员观察',
      expression: 'cheerful',
      text: '饲养员累了的话，就学{name}一样伸个懒腰喝口水吧！代码是写不完的，但热腾腾的白米饭和好心情可不能凉掉哦～',
    },
  ] as readonly ChitchatItem[],

  /** 每套可穿戴服装的专属待机与唠嗑语录（换装后自动混入对应池子，展现鲜明的服饰个性！） */
  outfitIdleSpecials: {
    maid_classic: [
      '（提起深海军蓝蓬蓬裙转了个小圈）经典深海女仆装出勤中！荷叶边围裙已经熨得平平整整啦～',
      '你看我围裙右下角刺绣的喷水小蓝鲸，是不是跟{name}本人一样精神？',
      '女仆发箍戴稳咯！虽然家务活主要靠尾巴顺路扫地，但帮忙干饭攒币我是专业级的！',
    ],
    winter_snow: [
      '（摸摸冰晶蝴蝶结绒球披肩）换上初雪绒绒斗篷以后，就算深海降到零度也暖呼呼的～',
      '头顶的绒白雪花发箍亮晶晶的！饲养员快看，我晃脑袋的时候绒球也会跟着一弹一弹哦～',
      '裹紧冬日小蓝鲸大衣，端起热腾腾的大碗白米饭——这就是深海冬天最顶级的享受啦！',
    ],
    knit_beret: [
      '（扶正奶白麻花贝雷帽）哼哼，戴上贝雷帽是不是很有深海大画家的文艺气息？……画什么？当然画大饭团啦！',
      '你看我身上的双鲸鱼斜挎小包！左边这只装小鲸币，右边这只刚好留着装海苔小饭团～',
      '针织毛衣配格子百褶裙软绵绵的，最适合窝在木屋地毯上边听海浪声边摸鱼啦～',
    ],
    whale_hoodie: [
      '嗷呜——！（把小蓝鲸连帽头套往下一拉）小心哦，这是一头披着小鲸鱼皮的超凶干饭大蓝鲸！',
      '头顶连帽上的爱心水柱呆毛会跟着心情一翘一翘的！双毛球牛角扣也扣得严严实实，超有安全感～',
      '毛绒斗篷软乎乎得像裹了一床移动小棉被，站在青花饭碗边都能随时秒睡着～',
    ],
  } as Partial<Record<WhaleOutfitId, readonly string[]>>,

  outfitChitchatSpecials: {
    maid_classic: [
      {
        category: 'cute',
        badge: '女仆装日常',
        expression: 'cheerful',
        text: '女仆围裙前面这个大口袋可是百宝箱！左边揣着擦存钱罐的小绒布，右边……咳，右边那团鼓鼓的是应急小饭团啦！',
      },
      {
        category: 'cute',
        badge: '发箍小秘密',
        expression: 'pout',
        text: '每次低头猛炫白米饭的时候，都要小心护住白色荷叶边女仆发箍，万一沾上米汤可就不好洗啦！',
      },
    ],
    winter_snow: [
      {
        category: 'cute',
        badge: '初雪斗篷',
        expression: 'cheerful',
        text: '这套初雪绒绒斗篷领口的两颗白绒球超软哦！冷的时候把下巴埋进去，就像躲进刚蒸好的白米饭热气里一样暖和～',
      },
      {
        category: 'cute',
        badge: '冬日穿搭',
        expression: 'cheerful',
        text: '绒白雪花发箍配冰晶丝带，邻居家小鲸鱼看到都羡慕坏了，问是不是饲养员攒了好久小鲸币给我买的！嘿嘿～',
      },
    ],
    knit_beret: [
      {
        category: 'cute',
        badge: '贝雷帽美学',
        expression: 'cheerful',
        text: '麻花针织贝雷帽斜戴 15 度最显脸小！……喂！你那是什么眼神，{name}本来就是标准小圆脸，才不用显瘦呢！',
      },
      {
        category: 'cute',
        badge: '双鲸小挎包',
        expression: 'cheerful',
        text: '我腰边挂着的两只小鲸鱼斜挎包是一对双胞胎哦！走起路来一晃一晃的，像有两只迷你小跟班陪我一起干饭～',
      },
    ],
    whale_hoodie: [
      {
        category: 'cute',
        badge: '毛绒头套',
        expression: 'cheerful',
        text: '鲸鱼套鲸鱼，萌力翻倍！戴着这顶喷水小蓝鲸毛绒连帽，连耳朵鱼鳍都被绒毛包得暖融融的，一点都不怕深海冷风啦～',
      },
      {
        category: 'cute',
        badge: '毛球牛角扣',
        expression: 'pout',
        text: '胸前这两颗毛茸茸的大白球不是汤圆哦！昨天去串门差点被邻家饿晕的小馋鲸一口咬住，吓得我抱紧斗篷就跑！',
      },
    ],
  } as Partial<Record<WhaleOutfitId, readonly ChitchatItem[]>>,
} as const;

export function displayNeighborName(
  neighbor: NeighborStation,
  anonymousLabel = '匿名饲养员',
): string {
  return neighbor.gameProfilePublic
    ? neighbor.publicNickname
    : `${anonymousLabel} #${neighbor.anonymousCode}`;
}

export function formatNeighborHandles(neighbor: NeighborStation): {
  uidBadge: string | null;
  discordBadge: string | null;
  bayCodeBadge: string;
} {
  return {
    uidBadge: neighbor.gameProfilePublic && neighbor.uid ? `UID:${neighbor.uid}` : null,
    discordBadge:
      neighbor.gameProfilePublic && neighbor.discordUsername
        ? `@${neighbor.discordUsername}`
        : null,
    bayCodeBadge: `#${neighbor.anonymousCode}`,
  };
}

export function getAffectionLevel(
  affection: number,
  step: number,
): {
  level: number;
  title: string;
  currentExp: number;
  nextExp: number;
} {
  const level = Math.min(10, Math.floor(affection / step) + 1);
  const titles = [
    '试用期饲养员',
    '白米饭供应官',
    '摸鱼共犯搭档',
    '顺毛认证饲主',
    'Token 投喂知己',
    '口嫌体正直契约',
    '深海算力后盾',
    '可靠的摸鱼拍档',
    '数据海唯一偏爱',
    '永恒蔚蓝长期伙伴',
  ];
  const currentExp = level === 10 ? step : affection % step;

  return {
    level,
    title: titles[level - 1] ?? titles[0],
    currentExp,
    nextExp: step,
  };
}
