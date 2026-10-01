import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ErrorState } from '@shared/components/States';
import { useFatFishText } from './copy';
import type { Level } from '@shared/fatfish/engine/types';
import { localValidation, type ImportedDraft } from './draft';

interface Example { id: string; title: string; url: string; content_hash: string }
export function ExamplePicker({ onImport }: { onImport(value: ImportedDraft): void }) {
  const text = useFatFishText();
  const [error, setError] = useState<unknown>(null);
  const [selectedID, setSelectedID] = useState('');
  const manifest = useQuery({ queryKey: ['fatfish', 'examples'], queryFn: async () => {
    const response = await fetch('/examples/fatfish/manifest.json', { credentials: 'same-origin' });
    if (!response.ok) throw new Error('Example manifest could not be loaded');
    return (await response.json() as { examples: Example[] }).examples;
  } });
  const load = async (example: Example): Promise<Level> => {
    const response = await fetch(example.url, { credentials: 'same-origin' });
    if (!response.ok) throw new Error('Example could not be loaded');
    const text = await response.text();
    if (new TextEncoder().encode(text).byteLength > 256 * 1024) throw new Error('Example exceeds 256 KiB');
    const level = JSON.parse(text) as Level;
    const invalid = localValidation(level);
    if (invalid) throw new Error(invalid);
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
    {manifest.isPending ? <p>{text('loading_examples')}</p> : manifest.error ? <ErrorState error={manifest.error} /> : <div className="fatfish-actions">
      <label>{text('example_level')}<select aria-label={text('example_level')} value={selected?.id ?? ''} onChange={(event) => setSelectedID(event.target.value)}>
        {manifest.data?.map((example) => <option key={example.id} value={example.id}>{example.title}</option>)}
      </select></label>
      {selected ? <><button type="button" onClick={() => void choose(selected)}>{selected.title}</button>
        <button type="button" onClick={() => void download(selected)}>{text('download_json')} · {selected.title}</button></> : null}
    </div>}
    {error ? <ErrorState error={error} /> : null}
  </div>;
}
