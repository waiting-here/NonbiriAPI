import { createHash } from 'node:crypto';
import { readFileSync, readdirSync } from 'node:fs';
import { join, resolve } from 'node:path';
import ts from 'typescript';
import type { Plugin } from 'vite';
import type { CopyEntry, Language, LocalizedLabel, ReviewSources } from './types';

const virtualId = 'virtual:nonbiri-gacha-copy-review';
const resolvedId = '\0' + virtualId;
const gachaDir = 'web/src/user/activities/gacha';
const catalogFile = 'internal/gacha/catalog.json';
const presetFile = 'internal/gacha/presets.json';
const artworkFile = 'internal/artwork/catalog.json';
const appearanceFile = 'internal/appearance/catalog.json';
const serviceFile = 'internal/gacha/service.go';
const voiceFile = gachaDir + '/voice.ts';
const artworkView = 'web/src/shared/artwork/ArtworkImage.tsx';

const screenAreas: Record<string, LocalizedLabel> = {
  'GachaApp.tsx': ['抽卡大厅、规则与记录', 'Summon, rules and history'],
  'GachaDialog.tsx': ['活动弹窗', 'Activity dialogs'],
  [artworkView]: ['美术提示', 'Artwork notices'],
  'GachaEntry.tsx': ['活动入口', 'Activity entry'],
  'GachaPage.tsx': ['活动入口', 'Activity entry'],
  'Collection.tsx': ['图鉴与主题回忆', 'Collection and theme memories'],
  'DrawReveal.tsx': ['抽卡演出', 'Draw reveal'],
  'Shop.tsx': ['神秘商店', 'Mystery shop'],
  'tomorrow/Artwork.tsx': ['卡面无障碍标签', 'Card artwork accessibility'],
};

const voiceFields: Record<string, LocalizedLabel> = {
  VOICE: ['初遇台词', 'First encounter'],
  VOICE_AGAIN: ['重逢台词', 'Reunion'],
  PRELUDE: ['登场序言', 'Reveal prelude'],
  EYEBROW: ['登场标题', 'Reveal heading'],
  WHISPER: ['翻牌提示', 'Card back whisper'],
  SUMMARY: ['结果寄语', 'Results message'],
};

const uiField: LocalizedLabel = ['界面文字', 'Interface text'];
const hasChinese = (text: string) => /\p{Script=Han}/u.test(text);
type Named = { id: string; name: string; name_en?: string; [key: string]: unknown };
type CatalogTheme = Named & { rarity: string; appearance?: string };
type CatalogCard = Named & { theme: string; character: string; rarity: string };
type EntryContext = Pick<
  CopyEntry,
  'category' | 'area' | 'field' | 'theme' | 'character' | 'rarity'
>;
type AddEntry = (entry: CopyEntry) => void;

function nameOf(item: Named): LocalizedLabel {
  return [item.name, item.name_en ?? item.name];
}

function leafText(value: unknown, key: string): [string, string][] {
  if (typeof value === 'string') return [[key, value]];
  if (value === null || typeof value !== 'object') return [];
  return Object.entries(value).flatMap(([child, item]) => leafText(item, key + '.' + child));
}

function syntaxTree(file: string, source: string) {
  return ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true);
}

function visit(node: ts.Node, callback: (node: ts.Node) => void) {
  callback(node);
  ts.forEachChild(node, (child) => visit(child, callback));
}

function placeholder(expression: ts.Expression): string {
  return String.fromCharCode(36) + '{' + expression.getText() + '}';
}

function expressionText(expression: ts.Expression): string | undefined {
  if (ts.isStringLiteralLike(expression)) return expression.text;
  if (ts.isTemplateExpression(expression)) {
    return (
      expression.head.text +
      expression.templateSpans
        .map((span) => placeholder(span.expression) + span.literal.text)
        .join('')
    );
  }
  if (
    ts.isBinaryExpression(expression) &&
    expression.operatorToken.kind === ts.SyntaxKind.PlusToken
  ) {
    const left = expressionText(expression.left);
    const right = expressionText(expression.right);
    if (left !== undefined || right !== undefined) {
      return (left ?? placeholder(expression.left)) + (right ?? placeholder(expression.right));
    }
  }
  return undefined;
}

function isCopyCall(node: ts.Node): boolean {
  return (
    ts.isCallExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === 't'
  );
}

function semanticRole(parts: readonly ts.Node[]): string {
  const tokens: string[] = [];
  function collect(node: ts.Node) {
    if (isCopyCall(node) || ts.isJsxText(node)) return;
    if (ts.isIdentifier(node) || ts.isStringLiteralLike(node) || ts.isNumericLiteral(node)) {
      tokens.push(node.text);
    } else {
      tokens.push(String(node.kind));
      ts.forEachChild(node, collect);
    }
  }
  parts.forEach(collect);
  return tokens.length
    ? createHash('sha256').update(JSON.stringify(tokens)).digest('hex').slice(0, 12)
    : '';
}

const semanticAttributes = new Set([
  'aria-labelledby',
  'data-state',
  'href',
  'key',
  'name',
  'onChange',
  'onClick',
  'onClose',
  'onPress',
  'to',
  'type',
  'value',
]);

function jsxRole(element: ts.JsxElement | ts.JsxSelfClosingElement): string {
  const tag = ts.isJsxElement(element) ? element.openingElement : element;
  const attributes = tag.attributes.properties
    .filter(
      (attribute): attribute is ts.JsxAttribute =>
        ts.isJsxAttribute(attribute) && semanticAttributes.has(attribute.name.getText()),
    )
    .sort((a, b) => a.name.getText().localeCompare(b.name.getText()));
  const name = tag.attributes.properties.find(
    (attribute): attribute is ts.JsxAttribute =>
      ts.isJsxAttribute(attribute) &&
      ['id', 'className'].includes(attribute.name.getText()) &&
      !!attribute.initializer &&
      ts.isStringLiteral(attribute.initializer),
  );
  const label =
    name?.initializer && ts.isStringLiteral(name.initializer)
      ? name.initializer.text.replace(/\s+/g, '.')
      : tag.tagName.getText();
  // Anonymous siblings often differ only in the data they present, such as SR and SSR counts.
  const children =
    !name && !attributes.length && ts.isJsxElement(element)
      ? element.children.flatMap((child) =>
          ts.isJsxElement(child) ? [...child.children] : [child],
        )
      : [];
  const expressions = children.flatMap((child): ts.Node[] => {
    if (ts.isJsxSelfClosingElement(child)) {
      return child.attributes.properties.filter(
        (attribute) => ts.isJsxAttribute(attribute) && attribute.name.getText() === 'value',
      );
    }
    if (
      ts.isJsxExpression(child) &&
      child.expression &&
      !ts.isStringLiteralLike(child.expression) &&
      !isCopyCall(child.expression)
    )
      return [child.expression];
    return [];
  });
  const role = semanticRole([...attributes, ...expressions]);
  return role ? label + '@' + role : label;
}

// Use control behavior and branch context as well as enclosing tags and declarations.
function anchor(node: ts.Node): string {
  const path: string[] = [];
  for (let child = node, parent = node.parent; parent; child = parent, parent = parent.parent) {
    if (ts.isJsxAttribute(parent)) path.push(parent.name.getText());
    if (ts.isJsxElement(parent) || ts.isJsxSelfClosingElement(parent)) path.push(jsxRole(parent));
    if (ts.isConditionalExpression(parent) && child !== parent.condition) {
      const branch = child === parent.whenTrue ? 'then' : 'else';
      path.push(branch + '@' + semanticRole([parent.condition]));
    }
    if (
      ts.isBinaryExpression(parent) &&
      parent.operatorToken.kind === ts.SyntaxKind.AmpersandAmpersandToken &&
      child === parent.right
    ) {
      path.push('when@' + semanticRole([parent.left]));
    }
    if (ts.isVariableDeclaration(parent) && ts.isIdentifier(parent.name))
      path.push(parent.name.text);
    if (ts.isPropertyAssignment(parent)) path.push(parent.name.getText());
    if (ts.isFunctionDeclaration(parent) && parent.name) {
      path.push(parent.name.text);
      break;
    }
  }
  return path.reverse().join('/') || 'module';
}

export function collectScreenCopy(file: string, source: string): CopyEntry[] {
  const tree = syntaxTree(file, source);
  const entries: CopyEntry[] = [];
  const section = file.startsWith(gachaDir + '/') ? file.slice(gachaDir.length + 1) : file;
  const area = screenAreas[section] ?? ['抽卡界面', 'Gacha interface'];
  const counts = new Map<string, number>();
  const slot = (kind: string, node: ts.Node) => {
    const base = kind + '/' + anchor(node);
    const occurrence = (counts.get(base) ?? 0) + 1;
    counts.set(base, occurrence);
    return base + '/' + occurrence;
  };
  const addText = (
    language: Language,
    original: string,
    key: string,
    node: ts.Node,
    field = uiField,
  ) => {
    const line = tree.getLineAndCharacterOfPosition(node.getStart(tree)).line + 1;
    entries.push({
      source: { file, key, line },
      language,
      original,
      category: 'screen',
      area,
      field,
    });
  };
  const addPair = (zh: ts.Expression, en: ts.Expression, key: string, field = uiField) => {
    const first = expressionText(zh);
    const second = expressionText(en);
    if (first !== undefined && second !== undefined && !(first === 'zh' && second === 'en')) {
      addText('zh', first, key + '/zh', zh, field);
      addText('en', second, key + '/en', en, field);
    }
  };
  const compareBranches = (en: ts.Expression, zh: ts.Expression, key: string) => {
    if (
      ts.isConditionalExpression(en) &&
      ts.isConditionalExpression(zh) &&
      en.condition.getText() === zh.condition.getText()
    ) {
      compareBranches(en.whenTrue, zh.whenTrue, key + '/true');
      compareBranches(en.whenFalse, zh.whenFalse, key + '/false');
    } else {
      addPair(zh, en, key);
    }
  };
  visit(tree, (node) => {
    if (
      ts.isCallExpression(node) &&
      node.expression.getText(tree) === 't' &&
      node.arguments.length === 2
    ) {
      addPair(node.arguments[0], node.arguments[1], slot('text', node));
    }
    if (ts.isConditionalExpression(node) && node.condition.getText(tree) === 'english') {
      compareBranches(node.whenTrue, node.whenFalse, slot('language', node));
    }
    if (
      ts.isPropertyAssignment(node) &&
      ts.isObjectLiteralExpression(node.parent) &&
      ts.isVariableDeclaration(node.parent.parent) &&
      node.parent.parent.name.getText(tree) === 'errorCopy' &&
      ts.isArrayLiteralExpression(node.initializer) &&
      node.initializer.elements.length === 2
    ) {
      addPair(
        node.initializer.elements[0],
        node.initializer.elements[1],
        'errorCopy/' + node.name.getText(tree),
        ['操作提示', 'Action message'],
      );
    }
    if (ts.isJsxText(node) && /[\p{L}\p{N}]/u.test(node.text)) {
      const original = node.text.trim().replace(/\r?\n\s*/g, ' ');
      addText(hasChinese(original) ? 'zh' : 'en', original, slot('jsx', node), node);
    }
    if (
      section === 'GachaDialog.tsx' &&
      ts.isBindingElement(node) &&
      ts.isIdentifier(node.name) &&
      node.name.text === 'closeLabel' &&
      node.initializer &&
      ts.isStringLiteral(node.initializer)
    ) {
      addText('zh', node.initializer.text, 'parameter/GachaDialog/closeLabel', node.initializer, [
        '无障碍标签',
        'Accessible label',
      ]);
    }
    if (
      ts.isJsxAttribute(node) &&
      ['aria-label', 'alt', 'title', 'closeLabel', 'placeholder'].includes(node.name.getText()) &&
      node.initializer &&
      ts.isStringLiteral(node.initializer) &&
      node.initializer.text.trim()
    ) {
      const original = node.initializer.text;
      addText(
        hasChinese(original) ? 'zh' : 'en',
        original,
        slot('attribute', node),
        node.initializer,
        ['无障碍标签', 'Accessible label'],
      );
    }
  });
  return entries;
}

function collectVoices(
  tree: ts.SourceFile,
  cards: ReadonlyMap<string, CatalogCard>,
  add: AddEntry,
) {
  visit(tree, (node) => {
    if (!ts.isVariableDeclaration(node) || !ts.isIdentifier(node.name) || !node.initializer) return;
    const group = node.name.text;
    if (!Object.hasOwn(voiceFields, group)) return;
    const addLines = (key: string, tuple: ts.ArrayLiteralExpression, offset = 0) => {
      const card = cards.get(key);
      const area: LocalizedLabel = card ? nameOf(card) : ['抽卡演出', 'Draw reveal'];
      tuple.elements.slice(offset).forEach((line, index) => {
        const order = tuple.elements.length - offset > 2 ? ' ' + (Math.floor(index / 2) + 1) : '';
        const [zh, en] = voiceFields[group];
        const original = expressionText(line);
        if (original !== undefined)
          add({
            source: { file: voiceFile, key: group + '.' + key + '.' + (index + offset) },
            original,
            language: index % 2 === 0 ? 'zh' : 'en',
            field: [zh + order, en + order],
            area,
            category: 'voice',
            theme: card?.theme,
            character: card?.character,
            rarity: card?.rarity ?? (['N', 'R', 'SR', 'SSR', 'SP'].includes(key) ? key : undefined),
          });
      });
    };
    if (ts.isObjectLiteralExpression(node.initializer)) {
      for (const property of node.initializer.properties) {
        if (
          !ts.isPropertyAssignment(property) ||
          !ts.isArrayLiteralExpression(property.initializer)
        )
          continue;
        const name = property.name;
        if (ts.isStringLiteral(name) || ts.isIdentifier(name))
          addLines(name.text, property.initializer);
      }
    }
    if (ts.isArrayLiteralExpression(node.initializer)) {
      for (const tuple of node.initializer.elements) {
        if (!ts.isArrayLiteralExpression(tuple)) continue;
        const key = expressionText(tuple.elements[0]);
        if (key) addLines(key, tuple, 1);
      }
    }
  });
}

export function collectCopyReviewSources(
  repository: string,
  watch: (path: string) => void = () => {},
): ReviewSources {
  const read = (file: string) => {
    const path = join(repository, file);
    watch(path);
    return readFileSync(path, 'utf8');
  };
  const entries: CopyEntry[] = [];
  const add: AddEntry = (entry) => {
    entries.push(entry);
  };
  const addValue = (
    file: string,
    key: string,
    language: Language,
    original: unknown,
    context: EntryContext,
  ) => {
    if (typeof original === 'string')
      add({ source: { file, key }, language, original, ...context });
  };
  const addFields = (
    file: string,
    path: string,
    item: Named,
    context: Omit<EntryContext, 'field'>,
    fields: [key: string, label: LocalizedLabel][],
  ) => {
    for (const [key, field] of fields) {
      addValue(file, path + '.' + key, 'zh', item[key], { ...context, field });
      addValue(file, path + '.' + key + '_en', 'en', item[key + '_en'], { ...context, field });
    }
  };

  const catalog = JSON.parse(read(catalogFile)) as {
    themes: CatalogTheme[];
    cards: CatalogCard[];
    characters: string[];
  };
  const themes = new Map(catalog.themes.map((theme) => [theme.id, theme]));
  const cards = new Map(catalog.cards.map((card) => [card.id, card]));
  for (const theme of catalog.themes) {
    addFields(
      catalogFile,
      'themes.' + theme.id,
      theme,
      {
        category: 'theme',
        area: nameOf(theme),
        theme: theme.id,
        rarity: theme.rarity,
      },
      [
        ['name', ['主题名称', 'Theme name']],
        ['tagline', ['主题寄语', 'Theme tagline']],
      ],
    );
  }
  for (const card of catalog.cards) {
    addFields(
      catalogFile,
      'cards.' + card.id,
      card,
      {
        category: 'card',
        area: nameOf(card),
        theme: card.theme,
        character: card.character,
        rarity: card.rarity,
      },
      [
        ['name', ['卡牌名称', 'Card name']],
        ['description', ['卡牌简介', 'Card description']],
        ['lore', ['卡牌故事', 'Card lore']],
        ['quote', ['卡牌引语', 'Card quote']],
      ],
    );
  }
  collectVoices(syntaxTree(voiceFile, read(voiceFile)), cards, add);

  const { presets } = JSON.parse(read(presetFile)) as {
    presets: (Named & { pool: Named & { cover: string } })[];
  };
  for (const preset of presets) {
    const theme = themes.get(preset.pool.cover);
    const context = {
      category: 'pool' as const,
      area: nameOf(preset.pool),
      theme: theme?.id,
      rarity: theme?.rarity,
    };
    addFields(presetFile, 'presets.' + preset.id, preset, context, [
      ['name', ['预设名称', 'Preset name']],
    ]);
    addFields(presetFile, 'presets.' + preset.id + '.pool', preset.pool, context, [
      ['name', ['奖池名称', 'Pool name']],
    ]);
  }
  const service = read(serviceFile);
  const permanent = service.match(
    /Pool\{ID: "permanent", Name: ("(?:\\.|[^"\\])*"), NameEN: ("(?:\\.|[^"\\])*")/,
  );
  if (!permanent) throw new Error('Could not locate the permanent pool names in ' + serviceFile);
  const area: LocalizedLabel = [JSON.parse(permanent[1]), JSON.parse(permanent[2])];
  const poolContext = {
    category: 'pool' as const,
    area,
    field: ['奖池名称', 'Pool name'] as LocalizedLabel,
  };
  addValue(serviceFile, 'permanentPool.Name', 'zh', area[0], poolContext);
  addValue(serviceFile, 'permanentPool.NameEN', 'en', area[1], poolContext);

  const { artworks } = JSON.parse(read(artworkFile)) as { artworks: Record<string, Named> };
  for (const [key, artwork] of Object.entries(artworks)) {
    if (!key.startsWith('cover:')) continue;
    const theme = themes.get(key.slice('cover:'.length));
    addFields(
      artworkFile,
      'artworks.' + key,
      artwork,
      {
        category: 'pool',
        area: nameOf(artwork),
        theme: theme?.id,
        rarity: theme?.rarity,
      },
      [['name', ['封面名称', 'Cover name']]],
    );
  }
  const appearances = JSON.parse(read(appearanceFile)) as Named[];
  for (const appearance of appearances) {
    const theme = catalog.themes.find((item) => item.appearance === appearance.id);
    if (!theme && appearance.id !== 'heart-moment') continue;
    addFields(
      appearanceFile,
      appearance.id,
      appearance,
      {
        category: 'theme',
        area: nameOf(appearance),
        theme: theme?.id,
        rarity: theme?.rarity,
      },
      [
        ['name', ['装扮名称', 'Appearance name']],
        ['description', ['装扮介绍', 'Appearance description']],
      ],
    );
  }

  for (const language of ['zh', 'en'] as const) {
    const userFile = 'web/src/user/i18n/' + language + '.json';
    const commonFile = 'web/src/shared/i18n/common/' + language + '.json';
    const user = JSON.parse(read(userFile));
    const common = JSON.parse(read(commonFile));
    for (const [key, original] of leafText(user.user.appearance, 'user.appearance')) {
      addValue(userFile, key, language, original, {
        category: 'screen',
        area: key.startsWith('user.appearance.moments.')
          ? ['心动时刻', 'Heartfelt moments']
          : ['主题装扮', 'Appearances'],
        field: uiField,
      });
    }
    addValue(commonFile, 'artwork.unavailable', language, common.artwork.unavailable, {
      category: 'screen',
      area: ['美术提示', 'Artwork notices'],
      field: uiField,
    });
  }

  const viewFiles = readdirSync(join(repository, gachaDir), { recursive: true, encoding: 'utf8' })
    .map((file) => file.replace(/\\/g, '/'))
    .filter((file) => file.endsWith('.tsx') && !/\.(test|spec)\.tsx$/.test(file))
    .sort();
  for (const viewFile of viewFiles) {
    const file = gachaDir + '/' + viewFile;
    entries.push(...collectScreenCopy(file, read(file)));
  }
  entries.push(...collectScreenCopy(artworkView, read(artworkView)));
  return {
    entries,
    themes: catalog.themes.map((theme) => ({ id: theme.id, name: nameOf(theme) })),
    characters: catalog.characters,
    rarities: [...new Set(catalog.cards.map((card) => card.rarity))],
  };
}

export function gachaCopyReviewPlugin(): Plugin {
  let repository: string;
  const sources = new Set<string>();
  return {
    name: 'nonbiri-gacha-copy-review',
    configResolved(config) {
      repository = resolve(config.root, '../../..');
    },
    resolveId(id) {
      if (id === virtualId) return resolvedId;
    },
    load(id) {
      if (id !== resolvedId) return;
      const data = collectCopyReviewSources(repository, (path) => {
        this.addWatchFile(path);
        sources.add(resolve(path));
      });
      return 'export default ' + JSON.stringify(data) + ';';
    },
    handleHotUpdate({ file, server, modules }) {
      if (!sources.has(resolve(file))) return;
      const review = server.moduleGraph.getModuleById(resolvedId);
      if (review) return [...modules, review];
    },
  };
}
