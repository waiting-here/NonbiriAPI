import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@shared/components/ui/Button';
import { Segmented } from '@shared/components/ui/Segmented';
import { Tabs } from '@shared/components/ui/Tabs';
import { PageHeader } from '@shared/components/States';
import { ThemeToggle } from '@shared/theme/ThemeToggle';
import { useTheme } from '@shared/theme/useTheme';
import { GachaDialog } from '../user/activities/gacha/GachaDialog';
import { highResolutionArt } from '../user/activities/gacha/art-resolution';
import { useDarkMode, useReducedMotion } from './controls';
import { entryById, entryName, type Mode, type View } from './fixture';
import { ReviewPanel, type Inspection } from './ReviewPanel';
import { SceneArtwork } from './SceneArtwork';

const selected = (key: string, fallback: string) => {
  const value = new URLSearchParams(location.search).get(key) ?? '';
  return entryById[value] ? value : fallback;
};
const initialView = (): View => {
  const value = new URLSearchParams(location.search).get('view');
  return value === 'cover' ||
    value === 'illustration' ||
    value === 'appearance' ||
    value === 'materials'
    ? value
    : 'card';
};

export function Preview() {
  const { i18n } = useTranslation();
  const { setTheme } = useTheme();
  const dark = useDarkMode();
  const reduced = useReducedMotion();
  const english = i18n.language.startsWith('en');
  const [left, setLeft] = useState(() => selected('left', 'tomorrow'));
  const [right, setRight] = useState(() => selected('right', 'l-chatgpt'));
  const [view, setView] = useState<View>(initialView);
  const [mode, setMode] = useState<Mode>(() =>
    new URLSearchParams(location.search).get('motion') === '0' ? 'still' : 'motion',
  );
  const [paused, setPaused] = useState(false);
  const [inspect, setInspect] = useState<Inspection | null>(null);
  const [initialTheme] = useState(() => new URLSearchParams(location.search).get('theme'));
  const animated = mode === 'motion' && !reduced;
  const t = (zh: string, en: string) => (english ? en : zh);
  useEffect(() => {
    if (initialTheme === 'light' || initialTheme === 'dark') setTheme(initialTheme);
  }, [initialTheme, setTheme]);
  useEffect(() => {
    const url = new URL(location.href);
    url.searchParams.set('left', left);
    url.searchParams.set('right', right);
    url.searchParams.set('view', view);
    url.searchParams.set('motion', mode === 'motion' ? '1' : '0');
    url.searchParams.set('lang', english ? 'en' : 'zh');
    url.searchParams.set('theme', dark ? 'dark' : 'light');
    document.documentElement.lang = english ? 'en' : 'zh-CN';
    history.replaceState(null, '', url);
  }, [left, right, view, mode, english, dark]);
  const tabs = [
    { value: 'card' as const, label: t('卡牌与台词', 'Cards & dialogue') },
    { value: 'cover' as const, label: t('奖池封面', 'Pool covers') },
    { value: 'illustration' as const, label: t('单人插画', 'Illustrations') },
    { value: 'appearance' as const, label: t('明暗装扮', 'Appearances') },
    { value: 'materials' as const, label: t('成品素材', 'Material files') },
  ].map((tab) => ({ ...tab, id: 'tab-' + tab.value, panelId: 'review-panel' }));
  return (
    <div className="sp-review gacha-app">
      <header className="sp-review__header">
        <PageHeader
          title={t('SP 成品对照', 'SP artwork review')}
          description={t(
            '未命名的明天：卡面、插画、封面与明暗装扮。',
            'Tomorrow, Still Unwritten: card, illustration, cover and appearances.',
          )}
          actions={
            <>
              <ThemeToggle />
              <Button onClick={() => void i18n.changeLanguage(english ? 'zh' : 'en')}>
                {english ? '中文' : 'English'}
              </Button>
            </>
          }
        />
      </header>
      <main className="sp-review__main">
        <div className="sp-review__toolbar">
          <div className="sp-review__actions" aria-label={t('快速对照', 'Comparison presets')}>
            <Button
              size="sm"
              onClick={() => {
                setLeft('tomorrow');
                setRight('l-chatgpt');
              }}
            >
              {t('新卡 / 星月', 'Tomorrow / Moon & Stars')}
            </Button>
            <Button
              size="sm"
              onClick={() => {
                setLeft('tomorrow');
                setRight('n-gemini');
              }}
            >
              {t('新卡 / 镜海', 'Tomorrow / Mirror Sea')}
            </Button>
            <Button
              size="sm"
              onClick={() => {
                setLeft('tomorrow');
                setRight('o-claude');
              }}
            >
              {t('新卡 / 琥珀', 'Tomorrow / Amber')}
            </Button>
            <Button
              size="sm"
              onClick={() => {
                setLeft(right);
                setRight(left);
              }}
            >
              {t('交换左右', 'Swap sides')}
            </Button>
          </div>
          <div className="sp-review__actions">
            <Segmented<Mode>
              label={t('画面模式', 'Art mode')}
              value={animated ? 'motion' : 'still'}
              options={[
                { value: 'motion', label: t('动态', 'Motion') },
                { value: 'still', label: t('静态回退', 'Static fallback') },
              ]}
              onChange={setMode}
              disabled={reduced}
            />
            {animated && view !== 'materials' && (
              <Button size="sm" aria-pressed={paused} onClick={() => setPaused(!paused)}>
                {paused ? t('继续动作', 'Resume motion') : t('暂停动作', 'Pause motion')}
              </Button>
            )}
          </div>
        </div>
        {reduced && (
          <p className="sp-review__note">
            {t(
              '已跟随系统减弱动效，显示静态画面。',
              'Following your system setting with static artwork.',
            )}
          </p>
        )}
        <Tabs<View>
          label={t('素材类型', 'Material type')}
          value={view}
          tabs={tabs}
          onChange={setView}
        />
        <div
          id="review-panel"
          role="tabpanel"
          aria-labelledby={'tab-' + view}
          className="sp-review__pair"
        >
          {[
            { key: 'left', id: left, set: setLeft, side: t('左侧卡牌', 'Left card') },
            { key: 'right', id: right, set: setRight, side: t('右侧卡牌', 'Right card') },
          ].map(({ key, id, set, side }) => (
            <ReviewPanel
              key={key}
              entry={entryById[id]}
              setEntry={set}
              side={side}
              view={view}
              animated={animated}
              dark={dark}
              english={english}
              paused={paused || inspect !== null}
              inspect={setInspect}
            />
          ))}
        </div>
        <p className="sp-review__note">
          {t(
            '点击画面可放大。装扮随明暗主题切换，也可打开独立窗口检查取景。',
            'Click artwork to inspect it. Appearances follow the color theme; open a full view to check framing.',
          )}
        </p>
      </main>
      <footer className="sp-review__footer">
        {t(
          '本地候选预览 · 新卡尚未加入正式奖池 · 六张已发布 SP 可选作对照',
          'Local candidate preview · No new live pool · Six published SP cards available for comparison',
        )}
      </footer>
      {inspect && (
        <GachaDialog
          title={entryName(inspect.entry, english)}
          onClose={() => setInspect(null)}
          className={'sp-review__inspect sp-review__inspect--' + inspect.variant}
          closeLabel={t('关闭', 'Close')}
        >
          <div className="sp-review__inspect-art">
            <SceneArtwork
              key={inspect.entry.id + inspect.variant}
              entry={inspect.entry}
              variant={inspect.variant}
              dark={dark}
              animated={animated}
              paused={paused}
              english={english}
              highResolution
              className="sp-review__painting"
            />
          </div>
          <div className="sp-review__actions">
            <Button
              disabled={reduced}
              onClick={() => setMode(mode === 'motion' ? 'still' : 'motion')}
            >
              {animated ? t('查看静态', 'View static') : t('播放动作', 'Play motion')}
            </Button>
            {animated && (
              <Button aria-pressed={paused} onClick={() => setPaused(!paused)}>
                {paused ? t('继续动作', 'Resume motion') : t('暂停动作', 'Pause motion')}
              </Button>
            )}
            <a
              href={highResolutionArt(
                inspect.variant === 'card' ? inspect.entry.art : inspect.entry[inspect.variant],
              )}
              target="_blank"
              rel="noreferrer"
            >
              {t('打开高清图', 'Open high-resolution image')}
            </a>
          </div>
        </GachaDialog>
      )}
    </div>
  );
}
