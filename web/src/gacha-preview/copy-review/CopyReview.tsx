import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { EmptyState, PageHeader } from '@shared/components/States';
import { Button } from '@shared/components/ui/Button';
import { Field } from '@shared/components/ui/Field';
import { FilterBar } from '@shared/components/ui/FilterBar';
import { Fold } from '@shared/components/ui/Fold';
import { Note } from '@shared/components/ui/Note';
import { Toggle } from '@shared/components/ui/Toggle';
import { PagePagination } from '@shared/operations/PagePagination';
import type { PageSize } from '@shared/operations/pageNumbers';
import { ThemeToggle } from '@shared/theme/ThemeToggle';
import { copyText } from '@shared/utils/clipboard';
import data from 'virtual:nonbiri-gacha-copy-review';
import {
  copyKey,
  emptyDraft,
  keepsPlaceholders,
  mergePackage,
  packageDraft,
  parsePackage,
  pendingKey,
  reconcileDraft,
  resolvePending,
  serializeDraft,
  storageKey,
  updateDraft,
  type ReviewDraft,
} from './drafts';
import { categories, labels, languages, type LabelKey } from './labels';
import type { CopyChange, CopyEntry, Language } from './types';
import './copy-review.css';

const sources = new Map(data.entries.map((entry) => [copyKey(entry), entry]));
const themes = new Map(data.themes.map((theme) => [theme.id, theme.name]));
const filterDefaults = {
  search: '',
  category: '',
  theme: '',
  character: '',
  language: '',
  rarity: '',
  changed: false,
};

type Filter = typeof filterDefaults;
type Translator = (key: LabelKey) => string;
type StoreIssue = 'storageError' | 'storageReadError';
type Notice = { text: string; tone: 'ok' | 'bad' };
type Options = [value: string, label: string][];
const entryTitle = (entry: CopyEntry, index: 0 | 1) =>
  [
    entry.area[index],
    entry.character && !entry.area[index].includes(entry.character) ? entry.character : undefined,
    entry.field[index],
  ]
    .filter(Boolean)
    .join(' · ');

function readDraft(): { draft: ReviewDraft; issue?: StoreIssue } {
  try {
    const raw = localStorage.getItem(storageKey);
    const draft = raw ? mergePackage(emptyDraft(), parsePackage(raw), sources).draft : emptyDraft();
    return { draft };
  } catch {
    return { draft: emptyDraft(), issue: 'storageReadError' };
  }
}

function saveDownload(draft: ReviewDraft) {
  const url = URL.createObjectURL(
    new Blob([serializeDraft(draft)], { type: 'application/json;charset=utf-8' }),
  );
  const link = document.createElement('a');
  link.href = url;
  link.download = 'gacha-copy-review.json';
  link.click();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}

function matchesFilters(entry: CopyEntry, filters: Filter, edited?: CopyChange) {
  if (filters.category && entry.category !== filters.category) return false;
  if (filters.theme && entry.theme !== filters.theme) return false;
  if (filters.character && entry.character !== filters.character) return false;
  if (filters.language && entry.language !== filters.language) return false;
  if (filters.rarity && entry.rarity !== filters.rarity) return false;
  if (filters.changed && !edited) return false;
  const query = filters.search.trim().toLowerCase();
  if (!query) return true;
  const theme = entry.theme ? themes.get(entry.theme) : undefined;
  return [
    entry.original,
    entry.character,
    entry.rarity,
    ...entry.area,
    ...entry.field,
    ...categories[entry.category],
    ...(theme ?? []),
  ].some((value) => value?.toLowerCase().includes(query));
}

function SelectFilter({
  label,
  all,
  value,
  options,
  change,
}: {
  label: string;
  all: string;
  value: string;
  options: Options;
  change: (value: string) => void;
}) {
  return (
    <Field label={label}>
      {(props) => (
        <select {...props} value={value} onChange={(event) => change(event.target.value)}>
          <option value="">{all}</option>
          {options.map(([id, option]) => (
            <option key={id} value={id}>
              {option}
            </option>
          ))}
        </select>
      )}
    </Field>
  );
}

function Original({
  label,
  children,
  language,
}: {
  label: string;
  children: ReactNode;
  language: Language;
}) {
  return (
    <div className="copy-review__original">
      <span className="nb-field__label">{label}</span>
      <p lang={language}>{children}</p>
    </div>
  );
}

function EntryEditor({
  entry,
  edited,
  index,
  label,
  onChange,
}: {
  entry: CopyEntry;
  edited?: CopyChange;
  index: 0 | 1;
  label: Translator;
  onChange: (value: string) => void;
}) {
  const title = entryTitle(entry, index);
  const value = edited?.value ?? entry.original;
  const error = edited && !keepsPlaceholders(edited) ? label('placeholders') : undefined;
  return (
    <li className="copy-review__entry">
      <div className="copy-review__entry-heading">
        <h3>{title}</h3>
        <span>
          {languages[entry.language][index]}
          {edited ? ' · ' + label('modified') : ''}
        </span>
      </div>
      <div className="copy-review__comparison">
        <Original label={label('original')} language={entry.language}>
          {entry.original}
        </Original>
        <Field label={label('draft')} error={error}>
          {(props) => (
            <textarea
              {...props}
              lang={entry.language}
              aria-label={
                title +
                ' · ' +
                languages[entry.language][index] +
                ' · ' +
                label('draft') +
                ' · ' +
                entry.original.slice(0, 64)
              }
              rows={Math.min(8, Math.max(2, entry.original.split('\n').length))}
              value={value}
              onChange={(event) => onChange(event.target.value)}
            />
          )}
        </Field>
      </div>
      {edited && (
        <Button type="button" variant="ghost" onClick={() => onChange(entry.original)}>
          {label('reset')}
        </Button>
      )}
    </li>
  );
}

function PendingEdits({
  draft,
  index,
  label,
  onResolve,
}: {
  draft: ReviewDraft;
  index: 0 | 1;
  label: Translator;
  onResolve: (change: CopyChange, source?: CopyEntry) => void;
}) {
  return (
    <Fold title={label('reviewConflicts')} meta={draft.pending.length.toLocaleString()} defaultOpen>
      <p className="copy-review__conflict-hint">{label('conflictHint')}</p>
      <ul className="copy-review__entries">
        {draft.pending.map((change) => {
          const key = copyKey(change);
          const source = sources.get(key);
          const local = draft.edits.get(key);
          const reason = !source
            ? 'sourceMissing'
            : source.original !== change.original
              ? 'sourceDrift'
              : local
                ? 'differentDraft'
                : undefined;
          return (
            <li className="copy-review__entry" key={pendingKey(change)}>
              <div className="copy-review__entry-heading">
                <h3>{source ? entryTitle(source, index) : label('missingEntry')}</h3>
                <span>{languages[change.language][index]}</span>
              </div>
              {reason && <p className="copy-review__conflict-reason">{label(reason)}</p>}
              <div className="copy-review__comparison">
                <Original label={label('previousOriginal')} language={change.language}>
                  {change.original}
                </Original>
                <Original label={label('incoming')} language={change.language}>
                  {change.value}
                </Original>
                {source && source.original !== change.original && (
                  <Original label={label('currentOriginal')} language={change.language}>
                    {source.original}
                  </Original>
                )}
                {local && (
                  <Original label={label('currentDraft')} language={change.language}>
                    {local.value}
                  </Original>
                )}
              </div>
              <div className="copy-review__actions">
                {source && (
                  <Button type="button" onClick={() => onResolve(change, source)}>
                    {label('replace')}
                  </Button>
                )}
                <Button type="button" variant="ghost" onClick={() => onResolve(change)}>
                  {label(local ? 'keep' : 'dismiss')}
                </Button>
              </div>
            </li>
          );
        })}
      </ul>
    </Fold>
  );
}

export function CopyReview({ backHref }: { backHref?: string } = {}) {
  const { i18n } = useTranslation();
  const [english, setEnglish] = useState(
    () => new URLSearchParams(location.search).get('lang') === 'en',
  );
  useEffect(() => {
    void i18n.changeLanguage(english ? 'en' : 'zh');
  }, [english, i18n]);
  const index = english ? 1 : 0;
  const label: Translator = (key) => labels[key][index];
  const [session, setSession] = useState(readDraft);
  const draftRef = useRef(session.draft);
  const savedDraft = useRef(session.draft);
  const saving = useRef(false);

  useEffect(() => {
    const sync = (event: StorageEvent) => {
      if (
        (event.key !== null && event.key !== storageKey) ||
        event.storageArea !== localStorage ||
        draftRef.current !== savedDraft.current
      )
        return;
      const loaded = readDraft();
      savedDraft.current = loaded.draft;
      draftRef.current = loaded.draft;
      setSession(loaded);
    };
    window.addEventListener('storage', sync);
    return () => window.removeEventListener('storage', sync);
  }, []);
  const [notice, setNotice] = useState<Notice>();
  const [filters, setFilters] = useState<Filter>(filterDefaults);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState<PageSize>(10);
  const fileInput = useRef<HTMLInputElement>(null);
  const results = useRef<HTMLHeadingElement>(null);

  const { draft, issue } = session;
  const modified = draft.edits.size;
  const hasEdits = modified + draft.pending.length > 0;
  const visible = data.entries.filter((entry) =>
    matchesFilters(entry, filters, draft.edits.get(copyKey(entry))),
  );
  const totalPages = Math.max(1, Math.ceil(visible.length / pageSize));
  const currentPage = Math.min(page, totalPages);
  const active = Object.values(filters).filter(Boolean).length;

  function persist(next: ReviewDraft) {
    if (next === draftRef.current) return;
    draftRef.current = next;
    setSession({ draft: next, issue });
    if (saving.current) return;
    saving.current = true;
    const fail = () => {
      saving.current = false;
      setSession({ draft: draftRef.current, issue: 'storageError' });
    };
    const commit = () => {
      try {
        const latest = readDraft().draft;
        const merged = reconcileDraft(savedDraft.current, draftRef.current, latest);
        localStorage.setItem(storageKey, JSON.stringify(packageDraft(merged)));
        savedDraft.current = merged;
        draftRef.current = merged;
        saving.current = false;
        setSession({ draft: merged });
      } catch {
        fail();
      }
    };
    if (navigator.locks) {
      void navigator.locks.request(storageKey, commit).catch(fail);
    } else {
      commit();
    }
  }

  async function importFile(file: File) {
    try {
      const document = parsePackage(await file.text());
      const {
        draft: next,
        added,
        conflicted,
        repeated,
      } = mergePackage(draftRef.current, document, sources);
      if (added > 0 || conflicted > 0) persist(next);
      setNotice({
        text: english
          ? 'Imported ' + added + ' · To review ' + conflicted + ' · Already present ' + repeated
          : '已导入 ' + added + ' 项 · 待核对 ' + conflicted + ' 项 · 已存在 ' + repeated + ' 项',
        tone: 'ok',
      });
    } catch {
      setNotice({ text: label('invalidFile'), tone: 'bad' });
    }
  }

  function setFilter<K extends keyof Filter>(key: K, value: Filter[K]) {
    setFilters((previous) => ({ ...previous, [key]: value }));
    setPage(1);
  }

  function clearFilters() {
    setFilters(filterDefaults);
    setPage(1);
  }

  const filterChoices: [keyof Omit<Filter, 'search' | 'changed'>, Options][] = [
    ['category', Object.entries(categories).map(([key, label]) => [key, label[index]])],
    ['theme', data.themes.map(({ id, name }) => [id, name[index]])],
    ['character', data.characters.map((character) => [character, character])],
    ['language', Object.entries(languages).map(([key, label]) => [key, label[index]])],
    ['rarity', data.rarities.map((rarity) => [rarity, rarity])],
  ];
  const noEdits = filters.changed && !modified;
  function focusResults() {
    results.current?.focus({ preventScroll: true });
    results.current?.parentElement?.scrollIntoView({ block: 'start' });
  }

  return (
    <main className="copy-review">
      <div className="copy-review__utilities">
        <ThemeToggle />
        <Button
          type="button"
          variant="ghost"
          onClick={() => {
            const url = new URL(location.href);
            url.searchParams.set('lang', english ? 'zh' : 'en');
            history.replaceState(null, '', url);
            setEnglish(!english);
          }}
        >
          {english ? '简体中文' : 'English'}
        </Button>
      </div>
      <PageHeader
        title={label('title')}
        description={label('instructions')}
        back={
          <a className="nb-btn nb-btn--ghost" href={backHref ?? (english ? '?lang=en' : '?')}>
            {label('back')}
          </a>
        }
        actions={
          <>
            <input
              ref={fileInput}
              type="file"
              accept=".json,application/json"
              hidden
              onChange={(event) => {
                const file = event.target.files?.[0];
                event.target.value = '';
                if (file) void importFile(file);
              }}
            />
            <Button type="button" onClick={() => fileInput.current?.click()}>
              {label('import')}
            </Button>
            <Button
              type="button"
              disabled={!hasEdits}
              onClick={async () => {
                const ok = await copyText(serializeDraft(draftRef.current));
                setNotice(
                  ok
                    ? { text: label('copyOk'), tone: 'ok' }
                    : { text: label('copyError'), tone: 'bad' },
                );
              }}
            >
              {label('copy')}
            </Button>
            <Button
              type="button"
              variant="primary"
              disabled={!hasEdits}
              onClick={() => saveDownload(draftRef.current)}
            >
              {label('download')}
            </Button>
          </>
        }
      />
      {issue && <Note tone="warn">{label(issue)}</Note>}
      {notice && <Note tone={notice.tone}>{notice.text}</Note>}
      {draft.pending.length > 0 && (
        <PendingEdits
          draft={draft}
          index={index}
          label={label}
          onResolve={(change, entry) => {
            persist(resolvePending(draftRef.current, change, entry));
          }}
        />
      )}
      <FilterBar
        as="div"
        ariaLabel={label('filters')}
        activeCount={active}
        search={
          <>
            <Field label={label('search')}>
              {(props) => (
                <input
                  {...props}
                  type="search"
                  value={filters.search}
                  placeholder={label('searchHint')}
                  onChange={(event) => setFilter('search', event.target.value)}
                />
              )}
            </Field>
            <Toggle
              label={label('changedOnly')}
              checked={filters.changed}
              onChange={(checked) => setFilter('changed', checked)}
            />
            {active > 0 && (
              <Button type="button" variant="ghost" onClick={clearFilters}>
                {label('clear')}
              </Button>
            )}
          </>
        }
        secondaryAlwaysVisible
        secondary={filterChoices.map(([key, options]) => (
          <SelectFilter
            key={key}
            label={label(key)}
            value={filters[key]}
            all={label('all')}
            options={options}
            change={(value) => setFilter(key, value)}
          />
        ))}
      />
      <section className="copy-review__results" aria-label={label('results')}>
        <h2 className="sr-only" tabIndex={-1} ref={results}>
          {label('results')}
        </h2>
        <div className="copy-review__count" aria-live="polite">
          {english
            ? visible.length.toLocaleString() + ' shown · ' + modified.toLocaleString() + ' edited'
            : '显示 ' +
              visible.length.toLocaleString() +
              ' 项 · 已修改 ' +
              modified.toLocaleString() +
              ' 项'}
        </div>
        {visible.length > 0 ? (
          <ul className="copy-review__entries">
            {visible.slice((currentPage - 1) * pageSize, currentPage * pageSize).map((entry) => (
              <EntryEditor
                key={copyKey(entry)}
                entry={entry}
                edited={draft.edits.get(copyKey(entry))}
                index={index}
                label={label}
                onChange={(value) => persist(updateDraft(draftRef.current, entry, value))}
              />
            ))}
          </ul>
        ) : (
          <EmptyState
            title={label(noEdits ? 'unchanged' : 'none')}
            body={label(noEdits ? 'unchangedBody' : 'noneBody')}
            illustration={noEdits ? 'empty' : 'search'}
            action={
              <Button type="button" onClick={clearFilters}>
                {label('clear')}
              </Button>
            }
          />
        )}
        <PagePagination
          alwaysShowJump
          metadata={{
            page: String(currentPage),
            page_size: pageSize,
            total_items: String(visible.length),
            total_pages: String(totalPages),
          }}
          onPageChange={(next) => {
            setPage(Number(next));
            focusResults();
          }}
          onPageSizeChange={(next) => {
            setPageSize(next);
            setPage(1);
            focusResults();
          }}
        />
      </section>
    </main>
  );
}
