import { parts } from './parts';
import { environments } from './scenery';
import type { ClassicCard, Rect } from './types';

export type ClassicArtLayer = {
  name: string;
  label: string;
  path: string;
  rect: Rect;
};

const labels: Readonly<Record<string, string>> = {
  'lantern-shell': '灯笼壳与上链',
  'star-upper-chain': '提杆下悬挂链',
  'star-left-drop': '星灯左坠链',
  'star-middle-drop': '星灯内侧坠链',
  'star-right-drop': '星灯右坠链',
  'ribbon-behind-fingers': '穿在手指后的发光带',
  'ribbon-fingers': '遮住丝带的指尖',
  'ribbon-over-palm': '覆在掌前的发光带',
  'ribbon-free-tail': '发光带自由端',
  'pure-front-veil': '前方透明长纱',
  'orrery-ring': '会转动的浑仪内环',
  'orrery-crossband': '固定横梁与支架',
  'ear-tassel': '耳侧流苏',
  'waist-tassels': '腰间双穗',
  'book-page': '书页',
  'hair-tassel': '发饰流苏',
  'pearl-drop': '珍珠吊饰',
  'palm-star': '掌心星饰',
  'flower-bag': '提花包',
  'fan-tassels': '团扇流苏',
  'orb-interior': '球内鲸',
  'star-tassel': '星灯穗',
  'staff-pendant': '杖饰挂坠',
  'orrery-charm': '发侧星仪挂件',
  'tail-charm': '尾饰挂坠',
  'lantern-drop': '灯下坠饰',
  body: '人物底层',
  'hair-left': '画面左侧长发',
  'hair-right': '画面右侧长发',
  'sleeve-left': '左袖',
  'sleeve-right': '右袖',
  'wide-sleeve': '宽袖',
  'wide-sleeve-left': '左宽袖',
  'wide-sleeve-right': '右宽袖',
  'petal-sleeve-left': '左花瓣袖',
  'petal-sleeve-right': '右花瓣袖',
  tail: '尾柄与鳍',
  lantern: '提灯',
  'lantern-fringe': '提灯流苏',
  'cloud-companion': '云伴',
  'cloud-tassel': '云伴挂坠',
  whale: '窗外鲸鱼',
  'bow-ribbons': '头饰缎带',
  'obi-tails': '腰带下摆',
  'rose-ribbon': '花杖缎带',
  'train-left': '左侧拖裙',
  'train-right': '右侧拖裙',
  'pinched-train': '提起的裙摆',
  'skirt-left': '左裙片',
  'skirt-right': '右裙片',
  skirt: '前裙',
  'outer-skirt-left': '左外裙',
  'outer-skirt-right': '右外裙',
  'overskirt-left': '左外裙',
  'overskirt-right': '右外裙',
  'right-overskirt': '右外裙',
  'ink-skirt': '墨纹裙片',
  'silk-panel-right': '右侧浅色裙片',
  'front-skirt': '前裙',
  'hem-centre': '正面裙摆',
  'veil-front': '前罩纱',
  'skirt-ribbons-left': '左裙长带',
  'skirt-ribbons-right': '右裙长带',
  'star-light-core': '提灯星芯',
  'left-paper': '左侧悬纸',
  'right-paper': '右侧悬纸',
  'left-cloudbank': '左部云层',
  'right-cloudbank': '右部云层',
  sunlight: '日光',
  'diagram-light': '几何光线',
  'flower-branch': '前景花枝',
  waterfalls: '瀑布',
  'left-bubble': '左侧气泡',
  'right-bubble': '右侧气泡',
  'center-column': '中柱遮挡',
  caustics: '水光',
  sconces: '墙灯',
  candlelight: '烛光',
  butterfly: '蝶',
  'spiral-galaxy': '远处星系',
  'orrery-glints': '浑仪亮部',
  'floor-glints': '地面反光',
  sky: '烟花亮纹',
  river: '河面反光',
  lanterns: '灯笼',
  foliage: '树叶',
  'moon-and-stars': '月光和星光',
  'stage-lights': '舞台灯火',
  orrery: '悬挂浑仪',
  'hanging-star': '悬挂星饰',
  petals: '近处花瓣',
};

function label(name: string) {
  if (name.startsWith('gauze-')) return (labels[name.slice('gauze-'.length)] ?? name) + '纱';
  return labels[name] ?? name;
}

export function classicArtLayers(id: ClassicCard, expanded = false): readonly ClassicArtLayer[] {
  const card = parts[id];
  const group = expanded ? card?.expanded : card?.portrait;
  const prefix = '/assets/gacha/motion/' + id + '/parts/' + (expanded ? 'expanded-' : '');
  const layers = (group ?? []).map((part) => ({
    name: part.name,
    label: label(part.name),
    rect: part.rect,
    path: prefix + part.name + '.webp',
  }));
  if (expanded || !environments[id]) return layers;
  const scenery = environments[id];
  const background: ClassicArtLayer = {
    name: 'scenery',
    label: '场景底板',
    path: '/assets/gacha/' + scenery.base,
    rect: [0, 0, 1024, 1536],
  };
  const scenes = scenery.parts.map((part, index) => {
    const name = part.path
      .split('/')
      .at(-1)!
      .replace(/\.webp$/, '');
    return {
      name: name + '-' + index,
      label: label(name === 'cloud' ? 'cloud-companion' : name),
      rect: part.rect,
      path: '/assets/gacha/' + part.path,
      front: part.front,
    };
  });
  return [
    background,
    ...scenes.filter((part) => !part.front),
    ...layers,
    ...scenes.filter((part) => part.front),
  ];
}
