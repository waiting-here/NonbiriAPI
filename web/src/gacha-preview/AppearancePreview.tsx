import { useState } from 'react';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { Button } from '@shared/components/ui/Button';
import { PageHeader } from '@shared/components/States';
import { Segmented } from '@shared/components/ui/Segmented';
import { DataTable } from '@shared/components/ui/DataTable';
import { Field } from '@shared/components/ui/Field';
import { Note } from '@shared/components/ui/Note';
import { Panel, PanelBody, PanelHead } from '@shared/components/ui/Panel';
import { Tabs } from '@shared/components/ui/Tabs';
import { Toggle } from '@shared/components/ui/Toggle';
import { appearances, useOptionalAppearance } from '../user/appearance/context';
import '@shared/styles/stations/user-shell.css';

const rows = [
  { id: 'one', model: 'chat-model', tokens: '12,480', success: true, streaming: true },
  { id: 'two', model: 'reasoning-model', tokens: '8,192', success: true, streaming: false },
  { id: 'three', model: 'vision-model', tokens: '2,048', success: false, streaming: true },
];

export function AppearancePreview({ english }: { english: boolean }) {
  const appearance = useOptionalAppearance();
  const [dialog, setDialog] = useState(false);
  const [enabled, setEnabled] = useState(true);
  const [saved, setSaved] = useState(false);
  const [query, setQuery] = useState('');
  const [filter, setFilter] = useState('all');
  const [error, setError] = useState<unknown>(null);
  const copy = (zh: string, en: string) => (english ? en : zh);
  const close = () => setDialog(false);
  return (
    <div className="nb-page appearance-preview">
      <PageHeader
        title={copy('主题装扮与页面可读性', 'Appearance and readability')}
        description={copy(
          '切换主题与明暗模式，试试表单、列表和弹窗。',
          'Compare themes and color modes with forms, lists, and dialogs.',
        )}
      />
      <Panel>
        <PanelHead
          title={copy('选择装扮', 'Choose an appearance')}
          description={copy(
            '复用用户站组件与样式；此页使用本地示例数据。',
            'Uses the user station’s components and styles with local sample data.',
          )}
        />
        <PanelBody>
          <Segmented
            label={copy('选择装扮', 'Choose an appearance')}
            value={appearance?.state.selected ?? 'default'}
            options={appearances.map((item) => ({
              value: item.id,
              label: english ? item.name_en : item.name,
            }))}
            disabled={appearance?.saving}
            onChange={(id) => {
              setError(null);
              void appearance?.select(id).catch(setError);
            }}
          />
          {error ? (
            <Note tone="bad">
              {copy('切换失败，请重试。', 'Could not switch. Please try again.')}
            </Note>
          ) : null}
        </PanelBody>
      </Panel>
      <div className="appearance-preview__columns">
        <Panel>
          <PanelHead
            title={copy('端点设置', 'Endpoint settings')}
            description={copy('编辑常用连接设置。', 'Edit connection settings.')}
          />
          <PanelBody>
            <form
              className="appearance-preview__form"
              onSubmit={(event) => {
                event.preventDefault();
                setSaved(true);
              }}
            >
              <Field
                label={copy('端点名称', 'Endpoint name')}
                help={copy(
                  '用一个容易辨认的名称区分不同服务。',
                  'Use a recognizable name for each service.',
                )}
              >
                {(props) => (
                  <input
                    {...props}
                    required
                    defaultValue={copy('我的常用服务', 'My everyday service')}
                  />
                )}
              </Field>
              <Field
                label={copy('服务地址', 'Service URL')}
                help={copy(
                  '示例地址，不会发送网络请求。',
                  'Example address. No requests will be sent.',
                )}
              >
                {(props) => (
                  <input {...props} type="url" defaultValue="https://api.example.com/v1" />
                )}
              </Field>
              <Field label={copy('请求超时', 'Request timeout')}>
                {(props) => (
                  <select {...props} defaultValue="120">
                    <option value="60">60 {copy('秒', 'seconds')}</option>
                    <option value="120">120 {copy('秒', 'seconds')}</option>
                    <option value="300">300 {copy('秒', 'seconds')}</option>
                  </select>
                )}
              </Field>
              <Toggle
                label={copy('启用端点', 'Enable endpoint')}
                description={copy(
                  '供自己的模型调用使用。',
                  'Available for your personal model requests.',
                )}
                checked={enabled}
                onChange={setEnabled}
              />
              <div className="nb-inline">
                <Button type="submit" variant="primary">
                  {copy('保存示例', 'Save sample')}
                </Button>
                <Button type="button" onClick={() => setDialog(true)}>
                  {copy('查看弹窗效果', 'Preview dialog')}
                </Button>
                <Button type="button" disabled>
                  {copy('不可用操作', 'Unavailable')}
                </Button>
              </div>
              {saved ? (
                <Note tone="ok">
                  {copy(
                    '示例设置已保存，本页以外的数据不会改变。',
                    'Sample saved. Data outside this page is unchanged.',
                  )}
                </Note>
              ) : null}
            </form>
          </PanelBody>
        </Panel>
        <Panel>
          <PanelHead
            title={copy('请求记录', 'Request history')}
            description={copy(
              '查看小字、数字与状态提示的显示效果。',
              'Check small text, numbers, and status indicators.',
            )}
          />
          <PanelBody>
            <div className="appearance-preview__form">
              <Field label={copy('搜索模型', 'Search models')}>
                {(props) => (
                  <input
                    {...props}
                    type="search"
                    value={query}
                    onChange={(event) => setQuery(event.target.value)}
                    placeholder={copy('输入模型名称', 'Enter a model name')}
                  />
                )}
              </Field>
              <Tabs
                label={copy('请求状态', 'Request status')}
                value={filter}
                onChange={setFilter}
                tabs={[
                  { value: 'all', label: copy('全部', 'All') },
                  { value: 'success', label: copy('成功', 'Successful') },
                  { value: 'failed', label: copy('失败', 'Failed') },
                ]}
              />
              <DataTable
                caption={copy('示例请求记录', 'Sample request history')}
                rowKey={(row) => row.id}
                rows={rows.filter(
                  (row) =>
                    row.model.includes(query) &&
                    (filter === 'all' || row.success === (filter === 'success')),
                )}
                columns={[
                  {
                    key: 'model',
                    header: copy('模型', 'Model'),
                    cell: 'title',
                    render: (row) => <strong>{row.model}</strong>,
                  },
                  {
                    key: 'mode',
                    header: copy('方式', 'Mode'),
                    mobileLabel: copy('方式', 'Mode'),
                    cell: 'meta',
                    render: (row) =>
                      row.streaming ? copy('流式', 'Streaming') : copy('非流式', 'Non-streaming'),
                  },
                  {
                    key: 'tokens',
                    header: 'Tokens',
                    mobileLabel: 'Tokens',
                    cell: 'meta',
                    align: 'num',
                    render: (row) => row.tokens,
                  },
                  {
                    key: 'status',
                    header: copy('状态', 'Status'),
                    mobileLabel: copy('状态', 'Status'),
                    cell: 'status',
                    render: (row) => (
                      <span className={row.success ? 'status-ok' : 'field-error'}>
                        {row.success
                          ? copy('成功', 'Success')
                          : copy('上游超时', 'Upstream timeout')}
                      </span>
                    ),
                  },
                ]}
              />
              <Note tone="warn">
                {copy(
                  '示例提示：部分模型暂时不可用，可稍后重试。',
                  'Sample notice: some models are temporarily unavailable. Try again later.',
                )}
              </Note>
            </div>
          </PanelBody>
        </Panel>
      </div>
      <ConfirmDialog
        open={dialog}
        title={copy('保存端点设置', 'Save endpoint settings')}
        description={copy(
          '这里展示实际用户站的弹窗、说明文字与按钮。背景继续播放，可检查内容是否清晰。',
          'This uses the station’s dialog, description, and buttons. The background keeps playing so you can check readability.',
        )}
        confirmLabel={copy('保存示例', 'Save sample')}
        onCancel={close}
        onConfirm={() => {
          setSaved(true);
          close();
        }}
      >
        <Field
          label={copy('备注', 'Note')}
          optional={copy('可选', 'Optional')}
          help={copy(
            '这段较浅的帮助文字也应该容易阅读。',
            'This muted help text should also remain readable.',
          )}
        >
          {(props) => (
            <textarea
              {...props}
              rows={3}
              defaultValue={copy('用于日常对话与写作。', 'For everyday conversation and writing.')}
            />
          )}
        </Field>
      </ConfirmDialog>
    </div>
  );
}
