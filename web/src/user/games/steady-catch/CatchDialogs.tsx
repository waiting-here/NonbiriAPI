import { useEffect, useRef, useState, type ReactNode } from 'react';
import { Icon } from '@shared/components/Icon';
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
  caught = [],
  initialScope = 'all',
  open,
  onClose,
}: {
  phrases: readonly Phrase[];
  caught?: readonly string[];
  initialScope?: 'all' | 'round';
  open: boolean;
  onClose: () => void;
}) {
  const t = useCatchText();
  const [search, setSearch] = useState(''),
    [category, setCategory] = useState('all');
  const [scope, setScope] = useState(initialScope);
  const [wasOpen, setWasOpen] = useState(open);
  if (open !== wasOpen) {
    setWasOpen(open);
    if (open) setScope(initialScope);
  }
  const collected = new Set(caught);
  const metadata = notes.phrases as Record<
    string,
    { family: string; evidence: string; sources: string[] }
  >;
  const sources = new Map(notes.sources.map((source) => [source.id, source]));
  const matches = phrases.filter(
    (p) =>
      (scope === 'all' || collected.has(p.id)) &&
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
      <div className="catalog-scope" role="group" aria-label={t('图鉴范围', 'Catalog scope')}>
        <button
          className="quiet"
          aria-pressed={scope === 'round'}
          onClick={() => setScope('round')}
        >
          {t('本局接住', 'This round')}
        </button>
        <button className="quiet" aria-pressed={scope === 'all'} onClick={() => setScope('all')}>
          {t('全部', 'All')}
        </button>
      </div>
      <div className="catalog-controls">
        <label className="search-field">
          <Icon name="search" />
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
              <p className="phrase-title">
                {collected.has(p.id) && (
                  <span
                    className="collected-check"
                    aria-label={t('本局已接住', 'Caught this round')}
                  >
                    ✓{' '}
                  </span>
                )}
                {p.text}
              </p>
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
          <p>
            {scope === 'round' && !caught.length
              ? t(
                  '本次打开页面后还没有接住卡片。',
                  'No catches recorded since this page was opened.',
                )
              : t('没有找到这个梗，试试别的词。', 'No phrases found. Try another word.')}
          </p>
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
