import { useEffect, useRef, useState } from 'react';
import { renderAmber } from '../user/activities/gacha/amber/renderer';

function tarEntry(name: string, data: Uint8Array<ArrayBuffer>) {
  const header = new Uint8Array(512);
  const text = (offset: number, value: string) =>
    header.set(new TextEncoder().encode(value), offset);
  text(0, name);
  text(100, '0000644\0');
  text(108, '0000000\0');
  text(116, '0000000\0');
  text(124, data.length.toString(8).padStart(11, '0') + '\0');
  text(136, '00000000000\0');
  header.fill(32, 148, 156);
  text(156, '0');
  text(257, 'ustar\0');
  text(263, '00');
  text(
    148,
    header
      .reduce((sum, n) => sum + n, 0)
      .toString(8)
      .padStart(6, '0') + '\0 ',
  );
  return [header, data, new Uint8Array((512 - (data.length % 512)) % 512)];
}

export function AmberExport() {
  const [status, setStatus] = useState('从同一渲染器导出琥珀静态资源。');
  const [busy, setBusy] = useState(false);
  const [url, setURL] = useState('');
  const active = useRef<AbortController | null>(null);
  useEffect(() => () => active.current?.abort(), []);
  useEffect(
    () => () => {
      if (url) URL.revokeObjectURL(url);
    },
    [url],
  );
  const build = async () => {
    const controller = new AbortController();
    active.current = controller;
    setBusy(true);
    setStatus('正在生成静态资源…');
    setURL('');
    try {
      const parts: Uint8Array<ArrayBuffer>[] = [];
      let count = 0;
      for (const high of [false, true]) {
        const prefix = high ? 'hires/' : '';
        for (const output of [
          'card',
          'thumb',
          'cover',
          'background',
          'hero',
          'appearance-light',
          'appearance-dark',
          'plate-light',
          'plate-dark',
        ]) {
          if (high && (output === 'thumb' || output.startsWith('plate-'))) continue;
          const plate = output.startsWith('plate-');
          const portrait = ['card', 'thumb', 'hero'].includes(output);
          const canvas = document.createElement('canvas');
          canvas.width = plate
            ? 1037
            : output === 'thumb'
              ? 256
              : portrait
                ? high
                  ? 2560
                  : 1024
                : high
                  ? 3840
                  : 1672;
          canvas.height = plate
            ? 1560
            : output === 'thumb'
              ? 384
              : portrait
                ? high
                  ? 3840
                  : 1536
                : high
                  ? 2160
                  : 941;
          const release = await renderAmber(canvas, {
            polished: true,
            landscape: !portrait,
            exportSize: true,
            frozenTime: 0,
            blink: 0,
            highResolution: high,
            dark: output.endsWith('dark'),
            characterOnly: output === 'hero' || plate,
            characterPlate: plate,
            backgroundOnly: output === 'background' || output.startsWith('appearance-'),
            variant: output.startsWith('appearance-') ? 'appearance' : 'illustration',
            signal: controller.signal,
          });
          try {
            if (controller.signal.aborted) return;
            const blob = await new Promise<Blob>((resolve, reject) =>
              canvas.toBlob(
                (value) => (value ? resolve(value) : reject(new Error('导出失败'))),
                'image/webp',
                0.96,
              ),
            );
            const data = new Uint8Array(await blob.arrayBuffer());
            const names =
              output === 'card'
                ? ['cards/o-claude.webp']
                : output === 'thumb'
                  ? ['thumbs/o-claude.webp']
                  : output === 'cover'
                    ? ['covers/o.webp', 'groups/o.webp']
                    : output === 'background'
                      ? ['backgrounds/o.webp']
                      : output === 'hero'
                        ? ['motion/o-claude/hero.webp']
                        : plate
                          ? ['motion/o-claude/appearance-hero-' + output.slice(6) + '.webp']
                          : ['appearances/amber-' + output.slice(11) + '.webp'];
            for (const name of names) {
              parts.push(...tarEntry(prefix + name, data));
              count++;
            }
            setStatus(`${count} 张 · ${output} · ${canvas.width} × ${canvas.height}`);
          } finally {
            release();
            canvas.width = 0;
            canvas.height = 0;
          }
        }
      }
      parts.push(new Uint8Array(1024));
      setURL(URL.createObjectURL(new Blob(parts, { type: 'application/x-tar' })));
      setStatus(`已生成 ${count} 张静态资源，可下载后检查。`);
    } catch (error) {
      setStatus(String(error));
    } finally {
      setBusy(false);
    }
  };
  return (
    <main className="amber-preview">
      <h1>琥珀静态资源导出</h1>
      <button disabled={busy} onClick={() => void build()}>
        生成静态资源包
      </button>
      <p role="status">{status}</p>
      {url && (
        <a href={url} download="amber-rendered.tar">
          下载静态资源包
        </a>
      )}
      <p>
        <a href="?view=amber">返回美术预览</a>
      </p>
    </main>
  );
}
