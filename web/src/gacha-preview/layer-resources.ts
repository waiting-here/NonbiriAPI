import { highResolutionArt } from '../user/activities/gacha/art-resolution';
import type { ClassicCard } from '../user/activities/gacha/classic/types';
import type { MythicID, MythicPortrait } from '../user/activities/gacha/mythic/layers';
import type { MaterialSceneId } from '../user/activities/gacha/romance/material-plates';
import type { LayerLoader, LayerResource } from './MotionAssets';

const labels: Readonly<Record<string, string>> = {
  body: '人物底层',
  front: '手与近处遮挡',
  scenery: '场景底板',
  companion: '陪伴物',
  'depth-cover': '前景遮挡',
  'water-mask': '水域范围',
  moon: '月球',
  earth: '地球',
  'm-air-scenery': '场景底板',
  'm-air-companion': '巨鲸',
  'm-air-cover': '近景山川与遮挡',
  'cloud-front-whale': '鲸前云层',
  'cloud-middle-left': '左侧中景云',
  'cloud-near-left': '左侧近景云',
  'adopted-source': '采用的人物原画',
  'environment-source': '采用的场景原画',
  'material-cut-mask': '材料分离范围',
  'occlusion-backing': '遮挡后方补底',
  'back-locks': '后方发束',
  'front-locks': '前方发束',
  'left-hair': '左侧头发',
  'right-hair': '右侧头发',
  'left-locks': '左侧发束',
  'right-locks': '右侧发束',
  'long-left-hair': '左侧长发',
  'left-upper-locks': '左上发束',
  'left-lower-locks': '左下发束',
  'left-gauze': '左侧薄纱',
  'right-gauze': '右侧薄纱',
  'left-gauze-train': '左侧纱裙拖尾',
  'right-gauze-train': '右侧纱裙拖尾',
  'left-inner-train': '左侧内裙拖尾',
  'right-cyan-train': '右侧青色拖尾',
  'right-train': '右侧拖尾',
  'near-train': '近处拖尾',
  'lower-ivory-train': '浅色下裙拖尾',
  'near-sleeve': '近处袖片',
  'far-sleeve': '远处袖片',
  'outer-sleeve': '外侧袖片',
  'reaching-sleeve': '伸手侧袖片',
  'flowing-sleeve': '飘动长袖',
  'far-ribbon': '远处飘带',
  'low-ribbon': '下方飘带',
  'left-ivory-ribbon': '左侧浅色长带',
  'blue-left-ribbon': '左侧蓝色长带',
  'left-sky-ribbon': '左侧天青飘带',
  'upper-trailing-ribbon': '上方长飘带',
  'middle-trailing-ribbon': '中部长飘带',
  'low-trailing-ribbon': '下方长飘带',
  'left-scarf': '左侧披纱',
  'hair-gem': '发侧宝石',
  'hair-tassel': '发饰流苏',
  'gold-tassel': '金色流苏',
  'outer-tassel': '外侧流苏',
  'left-astral-charm': '左侧星饰',
  'right-emerald': '右侧翡翠挂坠',
  'whale-tail': '鲸尾',
  'rear-hair-left': '左侧后发',
  'rear-hair-right': '右侧后发',
  'hair-left-outer': '左侧外发束',
  'hair-right-outer': '右侧外发束',
  'hair-left-upper': '左上发束',
  'hair-left-lower': '左下发束',
  'hair-right-upper': '右上发束',
  'hair-right-lower': '右下发束',
  'hair-right-ends': '右侧发梢',
  'sleeve-silk': '独立袖纱',
  'train-left': '左侧拖裙',
  'train-right': '右侧拖裙',
  'cape-lower': '披风下摆',
  'robe-left-hem': '长袍左下摆',
  'robe-right-hem': '长袍右下摆',
  'tabard-lower': '前衣下摆',
  'hair-ribbon-tail': '发侧缎带末端',
  'wrist-ribbon-tip': '腕边缎带末端',
  'tote-cloth': '手提包布面',
  'coat-left-tail': '左侧衣尾',
  'coat-right-tail': '右侧衣尾',
  'coat-inner-left': '左侧衣尾内衬',
  'skirt-front-hem': '前裙下摆',
  'left-short-cord': '左侧短绳',
  'left-short-metal-cap': '左侧短绳金属端',
  'left-long-cord': '左侧长绳',
  'left-long-metal-cap': '左侧长绳金属端',
  'right-cord': '右侧挂绳',
  'right-metal-cap': '右侧挂绳金属端',
  'sunset-clouds': '晚霞云层',
  'curtain-left-lower': '左侧窗帘下摆',
  'curtain-right-lower': '右侧窗帘下摆',
  'canopy-left-outer': '左侧外缘树叶',
  'canopy-left-reach': '左侧伸出树叶',
  'canopy-right': '右侧树叶',
  'sleeve-crystal-upper': '袖侧上方水晶',
  'sleeve-crystal-lower': '袖侧下方水晶',
  'head-ribbon-short': '发侧短缎带',
  'head-ribbon-upper': '发侧上方缎带',
  'head-ribbon-lower': '发侧下方缎带',
  'head-crystal-near': '近处发饰水晶',
  'head-crystal-far': '远处发饰水晶',
  'sleeve-left-free': '左侧自由袖片',
  'sleeve-right-free': '右侧自由袖片',
  'waist-bell': '腰间铃饰',
  'seal-ribbon-left': '左侧封印飘带',
  'seal-ribbon-right': '右侧封印飘带',
  'hair-ribbon-short': '发侧短飘带',
  'hair-ribbon-long': '发侧长飘带',
  'wrist-ribbon-left': '腕侧左飘带',
  'wrist-ribbon-right': '腕侧右飘带',
  'blouse-tie': '上衣系结',
  'window-clouds': '窗外云层',
  'river-right-reflection': '右侧河面反光',
  'river-left-reflection': '左侧河面反光',
  'curtain-gold-chain-tassel': '窗帘金链与流苏',
  'candle-flame-tall': '高烛台火焰',
  'candle-flame-short': '低烛台火焰',
  'coat-left-tail-outer': '左侧衣尾外层',
  'coat-left-tail-lining': '左侧衣尾内衬',
  'coat-right-tail-outer': '右侧衣尾外层',
  'coat-right-tail-lining': '右侧衣尾内衬',
  'coat-inner-left-outer': '左侧内衣片外层',
  'coat-inner-left-lining': '左侧内衣片衬里',
  'hair-ornament-chain': '发饰挂链',
  'hair-ornament-tassel': '发饰流苏',
};
const labeled = (layer: Omit<LayerResource, 'label'>): LayerResource => ({
  ...layer,
  label: labels[layer.name] ?? layer.name,
});
const quality = (layers: readonly LayerResource[], high: boolean) =>
  layers.map((layer) => ({ ...layer, path: high ? highResolutionArt(layer.path) : layer.path }));
const whole = (name: string, label: string, path: string): LayerResource => ({
  name,
  label,
  path,
});

export const loadLayerResources: LayerLoader = async (id, scope, high, signal) => {
  signal.throwIfAborted();
  if (/^[gij]-/.test(id)) {
    const { classicArtLayers } = await import('../user/activities/gacha/classic/art-layers');
    return quality(classicArtLayers(id as ClassicCard), high);
  }
  if (id === 's-chatgpt' || id === 's-claude') {
    const { sweetLayers, layerRect } =
      await import('../user/activities/gacha/sweet-hide-and-seek/layers');
    return quality(
      [
        {
          name: 'scenery',
          label: '场景底板',
          path: `/assets/gacha/motion/${id}/background.webp`,
          rect: [0, 0, 1024, 1536],
        },
        ...sweetLayers(id).map((layer) => ({
          name: layer.name,
          label: layer.label,
          rect: layerRect(layer),
          path: `/assets/gacha/motion/${id}/parts/${layer.name}.webp`,
        })),
      ],
      high,
    );
  }
  if (id === 'l-chatgpt' || id === 'm-deepseek') {
    const { registeredParts } = await import('../user/activities/gacha/mythic/layers');
    const subject: MythicID =
      scope === 'portrait' ? id : `${id === 'l-chatgpt' ? 'celestial' : 'wandering'}-${scope}`;
    const prefix =
      scope === 'portrait'
        ? `/assets/gacha/${high ? 'hires/' : ''}motion/${id}/parts/`
        : `/assets/gacha/appearances/layers/${high ? 'hires/' : ''}${subject}/parts/`;
    const character = registeredParts(subject).map((part) =>
      labeled({ name: part.name, rect: part.rect, path: prefix + part.name + '.webp' }),
    );
    if (scope === 'portrait') {
      const names =
        id === 'm-deepseek'
          ? ['m-air-scenery', 'm-air-companion', 'm-air-cover', 'water-mask']
          : ['scenery', 'companion', 'depth-cover', 'water-mask', 'moon', 'earth'];
      const environment = names.map((name) =>
        whole(name, labels[name] ?? name, prefix + name + '.webp'),
      );
      if (id === 'l-chatgpt') {
        const { celestialParts } = await import('../user/activities/gacha/mythic/celestial-parts');
        environment.push(
          ...celestialParts.map((part) => ({
            name: part.name,
            label: part.label,
            rect: part.rect,
            path: prefix + part.name + '.webp',
          })),
        );
      } else {
        const { wanderingAirLayers } =
          await import('../user/activities/gacha/mythic/wandering-air-layers');
        environment.push(
          ...wanderingAirLayers.map((part, index) => ({
            name: part.name,
            label: labels[part.name] ?? `飞鸟 ${index + 1}`,
            rect: part.rect,
            path: prefix + part.path,
          })),
        );
      }
      return [...environment, ...character];
    }
    const root = `/assets/gacha/appearances/layers/${high ? 'hires/' : ''}`;
    const environment = [
      whole('scenery', '场景底板', root + subject + '/environment/scenery.webp'),
      whole('cloud', '云层', root + 'cloud.webp'),
      ...(id === 'm-deepseek'
        ? [whole('whale', '巨鲸', root + 'whale.webp')]
        : [
            whole('moon', '月球', root + 'moon.webp'),
            whole('earth', '地球', root + 'earth.webp'),
            whole(
              'foreground',
              '建筑与近景遮挡',
              root + subject + '/environment/architecture-mask.webp',
            ),
            ...(scope === 'light'
              ? [
                  whole(
                    'clouds',
                    '前方云层范围',
                    `/assets/gacha/appearances/layers/${subject}-clouds.webp`,
                  ),
                ]
              : []),
          ]),
    ];
    const { gardenParts, gardenPath } = await import('../user/appearance/wallpaper-garden');
    const garden = gardenParts[subject as MythicPortrait].map((part) => ({
      name: part.name,
      label: part.label,
      rect: part.rect,
      path: gardenPath(subject as MythicPortrait, part.name, high),
    }));
    return [...environment, ...garden, ...character];
  }
  if (/^[nopqr]-/.test(id)) {
    const { materialPlateSources } =
      await import('../user/activities/gacha/romance/material-plates');
    const materialId = id as MaterialSceneId;
    const layers = await materialPlateSources(materialId, 'material-layers', signal);
    if (/^[pqr]-/.test(id)) {
      const environment = await materialPlateSources(
        materialId,
        `environment-${scope === 'dark' ? 'dark' : 'light'}`,
        signal,
      );
      layers.unshift(
        ...environment.map((layer) => ({
          ...layer,
          name: layer.name === 'adopted-source' ? 'environment-source' : layer.name,
        })),
      );
      return layers.map(labeled);
    }
    const root = `/assets/gacha/${high ? 'hires/' : ''}motion/${id}/`;
    const environment = [
      whole('scenery', '场景底板', root + `background-${scope === 'dark' ? 'dark' : 'light'}.webp`),
      ...(id === 'n-gemini'
        ? [whole('shadow', '接触阴影', root + 'shadow.webp')]
        : [
            whole('flags', '悬旗', root + 'flags.webp'),
            whole('lamps', '吊灯', root + 'lamps.webp'),
            whole('books', '浮书', root + 'books.webp'),
          ]),
    ];
    return [...environment, ...layers.map(labeled)];
  }
  return [];
};
