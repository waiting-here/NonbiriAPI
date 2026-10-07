import { useEffect, useRef, useState, type ReactNode } from 'react';
import type { Phrase } from './engine';
import notes from './catalog-notes.json';
import { useCatchText } from './copy';

export function CatchDialog({
  open,
  onClose,
  title,
  className,
  children,
  eyebrow,
  count,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  className: string;
  children: ReactNode;
  eyebrow?: string;
  count?: number;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const t = useCatchText();
  useEffect(() => {
    const dialog = ref.current!;
    if (open && !dialog.open) dialog.showModal();
    else if (!open && dialog.open) dialog.close();
  }, [open]);
  return (
    <dialog
      ref={ref}
      className={className}
      aria-label={title}
      onClose={onClose}
      onClick={(e) => {
        if (e.target !== e.currentTarget) return;
        const r = e.currentTarget.getBoundingClientRect();
        if (e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom)
          e.currentTarget.close();
      }}
    >
      <div className="dialog-top">
        {eyebrow || count !== undefined ? (
          <div>
            {eyebrow && <span className="eyebrow">{eyebrow}</span>}
            <h2>
              {title} {count !== undefined && <span>{count}</span>}
            </h2>
          </div>
        ) : (
          <h2>{title}</h2>
        )}
        <button
          className="close-dialog icon-button"
          aria-label={t('关闭', 'Close')}
          onClick={() => ref.current?.close()}
        >
          ×
        </button>
      </div>
      {children}
    </dialog>
  );
}

export function CatchCatalog({
  phrases,
  open,
  onClose,
}: {
  phrases: readonly Phrase[];
  open: boolean;
  onClose: () => void;
}) {
  const t = useCatchText();
  const [search, setSearch] = useState(''),
    [category, setCategory] = useState('all');
  const metadata = notes.phrases as Record<
    string,
    { family: string; evidence: string; sources: string[] }
  >;
  const sources = new Map(notes.sources.map((source) => [source.id, source]));
  const matches = phrases.filter(
    (p) =>
      (category === 'all' || p.category === category) &&
      [p.text, p.category, metadata[p.id]?.family ?? '']
        .join(' ')
        .toLowerCase()
        .includes(search.trim().toLowerCase()),
  );
  const observed = phrases.filter((phrase) => metadata[phrase.id]?.evidence === 'observed').length;
  return (
    <CatchDialog
      open={open}
      onClose={onClose}
      title={t('八股梗图鉴', 'Phrase catalog')}
      eyebrow={t('接物机的语料仓库', 'The catcher’s phrase library')}
      count={phrases.length}
      className="catalog-dialog"
    >
      <p className="catalog-note">
        {observed}
        {t(' 个有出处的短语／句式，', ' sourced phrases, ')}
        {phrases.length - observed}
        {t(
          ' 个游戏改写。模型标签只表示社区联想，不代表独占、起源或统计频率。',
          ' game adaptations. Model labels describe community associations, not exclusive origins or measured frequency.',
        )}
      </p>
      <div className="catalog-controls">
        <label className="search-field">
          <span aria-hidden="true">⌕</span>
          <input
            type="search"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={t('找一个熟悉的梗…', 'Find a familiar phrase…')}
            aria-label={t('搜索八股梗', 'Search phrases')}
          />
        </label>
        <select
          value={category}
          onChange={(e) => setCategory(e.target.value)}
          aria-label={t('按类别筛选', 'Filter by category')}
        >
          <option value="all">{t('全部类别', 'All categories')}</option>
          {[...new Set(phrases.map((p) => p.category))].map((c) => (
            <option key={c} value={c}>
              {c}
            </option>
          ))}
        </select>
      </div>
      <div className="catalog-meta">
        <span>
          {matches.length} {t('张卡片', 'cards')}
        </span>
        <span>
          {t('社区例子', 'Community')} <i className="dot observed" /> {t('游戏改写', 'Adaptation')}{' '}
          <i className="dot adapted" />
        </span>
      </div>
      <div className="catalog-list">
        {matches.map((p) => {
          const meta = metadata[p.id];
          return (
            <article className="phrase-tile" key={p.id}>
              <p className="phrase-title">{p.text}</p>
              <small>
                <i className={'dot ' + (meta?.evidence === 'observed' ? 'observed' : 'adapted')} />
                {meta?.evidence === 'observed'
                  ? t('社区例子', 'Community example') + ' · ' + meta.family
                  : t('游戏改写', 'Game adaptation')}
              </small>
              <p>{p.category}</p>
              <div className="source-tag">
                {meta?.sources.map((id) => {
                  const source = sources.get(id);
                  return source ? (
                    <a
                      key={id}
                      href={source.url}
                      target="_blank"
                      rel="noopener noreferrer"
                      title={source.title}
                    >
                      {id}
                    </a>
                  ) : null;
                })}
              </div>
            </article>
          );
        })}
        {!matches.length && (
          <p>{t('没有找到这个梗，试试别的词。', 'No phrases found. Try another word.')}</p>
        )}
      </div>
      <details className="sources">
        <summary>
          {t('来源与整理说明', 'Sources and notes')} · {notes.sources.length}
        </summary>
        <p>
          {t(
            '这些普通表达只有在重复滥用时才构成这里的笑点，不能据此判定一段文章由 AI 写作。来源中的长句已节选为短卡片；游戏改写单独标记。',
            'The humor comes from overusing everyday expressions. They do not establish that a text was AI-written. Short excerpts and game adaptations are distinguished.',
          )}
        </p>
        <ol>
          {notes.sources.map((s) => (
            <li key={s.id}>
              <a href={s.url} target="_blank" rel="noopener noreferrer">
                {s.id} · {s.title}
              </a>
              <small>{s.note}</small>
            </li>
          ))}
        </ol>
      </details>
    </CatchDialog>
  );
}

export function CatchHelp({ open, onClose }: { open: boolean; onClose: () => void }) {
  const t = useCatchText();
  return (
    <CatchDialog
      open={open}
      onClose={onClose}
      title={t('怎么稳稳接住', 'How to catch steadily')}
      className="help-dialog"
    >
      <p>
        {t(
          '移动角色，把接物垫放在卡片下方。卡片经过虚线所在高度时，垫子接到卡片就算成功。',
          'Move the character to place the catching pad below a card. Catch it as it crosses the dotted line.',
        )}
      </p>
      <div className="help-controls">
        <p>
          <b>{t('电脑', 'Desktop')}</b>
          {t(
            '在场内移动鼠标／拖动；← → 或 A D 移动；空格护场；P 或 Esc 暂停。',
            'Move or drag the mouse; use arrows or A/D, Space for shield, P or Esc to pause.',
          )}
        </p>
        <p>
          <b>{t('手机', 'Mobile')}</b>
          {t(
            '按住场内左右滑动，或长按下方方向按钮。',
            'Drag across the field or hold a direction button below.',
          )}
        </p>
      </div>
      <p>
        {t(
          '白卡 10 分，金卡 20 分。每连接 5 句提升倍率，最高 ×3。漏接或受伤中断连击；漏接不扣耐心。道具不会中断连击。',
          'White cards give 10 points, gold cards 20. Every five catches increase the multiplier up to ×3. Misses or damage reset the combo; misses do not cost health. Power-ups preserve the combo.',
        )}
      </p>
      <h3>{t('道具', 'Power-ups')}</h3>
      <ul className="prop-list">
        {[
          [
            t('上下文护盾', 'Context shield'),
            t('抵挡错误卡，持续 7 秒', 'Blocks hazards for 7 seconds'),
          ],
          [
            t('低温采样', 'Low temperature'),
            t('全部卡片降速，持续 7 秒', 'Slows cards for 7 seconds'),
          ],
          [
            t('注意力磁铁', 'Attention magnet'),
            t('吸引附近的八股卡，持续 7 秒', 'Attracts nearby phrases for 7 seconds'),
          ],
          [
            t('Token 翻倍', 'Double tokens'),
            t('接物分数翻倍，持续 7 秒', 'Doubles scores for 7 seconds'),
          ],
          [
            t('重新生成', 'Regenerate'),
            t('恢复 1 点耐心，上限 5 点', 'Restores 1 health, up to 5'),
          ],
        ].map(([name, description]) => (
          <li key={name}>
            <b>{name}</b>
            <span>{description}</span>
          </li>
        ))}
      </ul>
      <p>
        {t(
          '稳稳护场：每接 10 句充满一次，按按钮或空格释放 4 秒护盾。满充能后可保留到需要时。',
          'Every 10 catches charge a four-second shield. Press the button or Space when you need it.',
        )}
      </p>
      <p>
        {t(
          '坚持 90 秒、保有耐心并拿到 600 分即通关。切到其他窗口会自动暂停。',
          'Survive 90 seconds with health remaining and 600 points to clear. Switching windows pauses the game.',
        )}
      </p>
      <p>
        {t(
          '门票先扣游戏积分，不足补通用积分。仅首次通关发放奖励；局内分数不是钱包积分。正常结束或放弃不退票，系统中止原路退票。暂停与离线不推进游戏，本局从创建起保留 30 分钟。',
          'Entry uses game credits first, then general credits. Only the first clear gives a reward; the game score is not wallet credit. Completion and abandonment do not refund entry; system cancellation does. Pausing and going offline stop play. A session lasts 30 minutes from creation.',
        )}
      </p>
    </CatchDialog>
  );
}
