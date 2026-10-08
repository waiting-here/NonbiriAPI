import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from 'react';
import { Card, ErrorState } from '@shared/components/States';
import { usePictureBookText } from '@shared/picturebook/copy';
import { useImageOperation } from '@shared/picturebook/useImageOperation';
import { saveUpstream, type Upstream, type UpstreamInput } from './adminApi';

export interface UpstreamDraftHandle {
  saveDraft: () => Promise<boolean>;
  discardDraft: () => void;
}

export const UpstreamForm = forwardRef<
  UpstreamDraftHandle,
  {
    readonly value: Upstream;
    readonly onSaved: (value: { revision: string }) => void;
    readonly onReload: () => void;
    readonly onDirty?: (dirty: boolean) => void;
    readonly onLocked?: (locked: boolean) => void;
    readonly disabled?: boolean;
  }
>(function UpstreamForm({ value, onSaved, onReload, onDirty, onLocked, disabled = false }, ref) {
  const t = usePictureBookText();
  const [baseURL, setBaseURL] = useState(value.base_url),
    [secret, setSecret] = useState(''),
    [replace, setReplace] = useState(!value.secret_set);
  const [saved, setSaved] = useState(false);
  const form = useRef<HTMLFormElement>(null);
  const save = useImageOperation('admin', saveUpstream);
  const dirty =
    !saved &&
    (save.locked || baseURL !== value.base_url || secret !== '' || replace !== !value.secret_set);
  useEffect(() => onDirty?.(dirty), [dirty, onDirty]);
  useEffect(() => onLocked?.(save.locked), [save.locked, onLocked]);
  const submit = async (): Promise<boolean> => {
    if (saved) return true;
    if (save.pending) return false;
    if (!save.input && !form.current?.reportValidity()) return false;
    const input: UpstreamInput = save.input ?? {
      expected_revision: value.revision,
      base_url: baseURL.trim(),
      secret: replace ? { mode: 'replace', value: secret } : { mode: 'keep' },
    };
    let accepted = false;
    await save.run(input, (result) => {
      accepted = true;
      setSecret('');
      setSaved(true);
      onSaved(result);
    });
    return accepted;
  };
  const discardDraft = () => {
    if (save.locked) return;
    setBaseURL(value.base_url);
    setSecret('');
    setReplace(!value.secret_set);
  };
  useImperativeHandle(ref, () => ({ saveDraft: submit, discardDraft }));
  return (
    <Card>
      <h2>{t('图片生成服务', 'Image generation service')}</h2>
      <p>
        {t(
          '填写服务地址和密钥，保存后拉取模型目录。仅适配特定规格的上游服务。',
          'Enter the service URL and key, save, then refresh the model catalog. Only a specific upstream service format is supported.',
        )}
      </p>
      <p>
        {t(
          '配置只供管理员查看。密钥只写入，不回显。保存的新配置用于新提交；已受理任务继续使用其原配置。',
          'Only administrators can view this configuration. Stored keys are never returned. New settings apply to new submissions; accepted tasks retain their original configuration.',
        )}
      </p>
      <form
        ref={form}
        className="picturebook-form"
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        <fieldset disabled={save.locked || saved || disabled}>
          <label>
            {t('服务地址', 'Service base URL')}
            <input
              type="url"
              required
              maxLength={4096}
              value={baseURL}
              placeholder="https://images.example.com/v1"
              onChange={(event) => setBaseURL(event.target.value)}
            />
          </label>
          {value.secret_set ? (
            <label className="picturebook-checkbox">
              <input
                type="checkbox"
                checked={replace}
                onChange={(event) => setReplace(event.target.checked)}
              />
              {t('替换已保存的密钥', 'Replace stored key')}
            </label>
          ) : null}
          {replace ? (
            <label>
              {t('活动专用密钥', 'Activity key')}
              <input
                type="password"
                autoComplete="new-password"
                required
                maxLength={65536}
                value={secret}
                onChange={(event) => setSecret(event.target.value)}
              />
            </label>
          ) : (
            <p>{t('保留已保存的密钥。', 'The existing key will be retained.')}</p>
          )}
        </fieldset>
        {disabled ? (
          <p role="status">
            {t(
              '请先保存或放弃模型改动，再修改服务。',
              'Save or discard model changes before editing the service.',
            )}
          </p>
        ) : null}
        {save.uncertain ? (
          <p role="status">
            {t(
              '保存结果尚未确认。请重试同一次保存。',
              'The save result is unconfirmed. Retry the same save.',
            )}
          </p>
        ) : null}
        {saved ? (
          <p role="status">
            {t(
              '已保存。正在重新读取配置；若未刷新，请点击重新读取。',
              'Saved. Reloading settings; use Reload if they do not refresh.',
            )}
          </p>
        ) : null}
        {save.error ? <ErrorState error={save.error} /> : null}
        <div className="picturebook-actions">
          <button
            className="nb-btn nb-btn--primary"
            type="submit"
            disabled={save.pending || saved || disabled}
          >
            {save.uncertain
              ? t('重试同一次保存', 'Retry the same save')
              : t('保存服务配置', 'Save service settings')}
          </button>
          <button
            className="nb-btn nb-btn--secondary"
            type="button"
            disabled={save.locked || dirty || disabled}
            onClick={onReload}
          >
            {t('重新读取已保存配置', 'Reload saved settings')}
          </button>
          {dirty ? (
            <button
              className="nb-btn nb-btn--secondary"
              type="button"
              disabled={save.locked || disabled}
              onClick={discardDraft}
            >
              {t('放弃服务草稿', 'Discard service draft')}
            </button>
          ) : null}
        </div>
      </form>
    </Card>
  );
});
