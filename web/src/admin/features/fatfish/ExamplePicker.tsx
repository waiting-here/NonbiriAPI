import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ErrorState } from '@shared/components/States';
import { useActivityText } from '@shared/limitedactivities/copy';
import { contentHash } from '@shared/fatfish/engine/canonical';
import type { Level } from '@shared/fatfish/engine/types';
import { localValidation, type ImportedDraft } from './draft';

interface Example { id: string; title: string; url: string; content_hash: string }
const filename = /^\/examples\/fatfish\/[A-Za-z0-9._-]+\.fatfish\.json$/;
function decodeManifest(value: unknown): Example[] {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Example manifest is invalid');
  const root = value as Record<string, unknown>;
  const items = root.examples;
  if (root.format !== 'nonbiri-fatfish-examples' || root.version !== 1 || !Array.isArray(items) || items.length !== 8)
    throw new Error('Example manifest is invalid');
  const seen = new Set<string>();
  return items.map((raw) => {
    if (!raw || typeof raw !== 'object') throw new Error('Example manifest entry is invalid');
    const item = raw as Record<string, unknown>;
    if (typeof item.id !== 'string' || !/^[A-Za-z0-9_-]{1,64}$/.test(item.id) || seen.has(item.id)
      || typeof item.title !== 'string' || item.title.length < 1 || item.title.length > 128
      || typeof item.url !== 'string' || !filename.test(item.url)
      || typeof item.content_hash !== 'string' || !/^[a-f0-9]{64}$/.test(item.content_hash))
      throw new Error('Example manifest entry is invalid');
    seen.add(item.id);
    return item as unknown as Example;
  });
}
export function ExamplePicker({ onImport }: { onImport(value: ImportedDraft): void }) {
  const t = useActivityText();
  const [error, setError] = useState<unknown>(null);
  const [selectedID, setSelectedID] = useState('');
  const manifest = useQuery({ queryKey: ['fatfish', 'examples'], queryFn: async () => {
    const response = await fetch('/examples/fatfish/manifest.json', { credentials: 'same-origin' });
    if (!response.ok) throw new Error('Example manifest could not be loaded');
    return decodeManifest(await response.json());
  } });
  const load = async (example: Example): Promise<Level> => {
    const response = await fetch(example.url, { credentials: 'same-origin' });
    if (!response.ok) throw new Error('Example could not be loaded');
    const text = await response.text();
    if (new TextEncoder().encode(text).byteLength > 256 * 1024) throw new Error('Example exceeds 256 KiB');
    const level = JSON.parse(text) as Level;
    const invalid = localValidation(level);
    if (invalid) throw new Error(invalid);
    if (contentHash(level) !== example.content_hash) throw new Error('Example content hash does not match the manifest');
    return level;
  };
  const choose = async (example: Example) => {
    setError(null);
    try {
      const level = await load(example);
      onImport({ title: example.title, description: '', level, pendingFishCount: 0 });
    } catch (cause) { setError(cause); }
  };
  const download = async (example: Example) => {
    setError(null);
    try {
      const level = await load(example);
      const blob = new Blob([JSON.stringify(level, null, 2) + '\n'], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement('a');
      anchor.href = url; anchor.download = `${example.id}.fatfish.json`; document.body.append(anchor); anchor.click(); anchor.remove();
      setTimeout(() => URL.revokeObjectURL(url), 0);
    } catch (cause) { setError(cause); }
  };
  const selected = manifest.data?.find((item) => item.id === selectedID) ?? manifest.data?.[0];
  return <div className="fatfish-examples">
    {manifest.isPending ? <p>{t('读取示例目录…', 'Loading examples…')}</p> : manifest.error ? <ErrorState error={manifest.error} /> : <div className="fatfish-actions">
      <label>{t('示例关卡', 'Example level')}<select aria-label={t('示例关卡', 'Example level')} value={selected?.id ?? ''} onChange={(event) => setSelectedID(event.target.value)}>
        {manifest.data?.map((example) => <option key={example.id} value={example.id}>{example.title}</option>)}
      </select></label>
      {selected ? <><button type="button" onClick={() => void choose(selected)}>{selected.title}</button>
        <button type="button" onClick={() => void download(selected)}>{t('下载 JSON', 'Download JSON')} · {selected.title}</button></> : null}
    </div>}
    {error ? <ErrorState error={error} /> : null}
  </div>;
}
