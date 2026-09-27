import { useCallback } from 'react';
import { useTranslation } from 'react-i18next';

const copy = {
  title: ['Request headers and body', '请求头与请求主体'],
  description: [
    'Configure only fields supported by the selected connector. Saved values are hidden after saving.',
    '仅配置当前连接器支持的字段。保存后配置值不会再次显示。',
  ],
  openaiSupport: [
    'OpenAI-compatible chat and embeddings preserve other bounded JSON fields. Exclude an unsupported client field explicitly, then set any replacement field here.',
    'OpenAI 兼容聊天与向量调用会保留其他有界 JSON 字段。客户端字段不受上游支持时，请先明确排除，再在此配置替代字段。',
  ],
  convertedSupport: [
    'Anthropic and Gateway convert supported OpenAI fields. Extra client fields reach the native request only when their paths are declared below.',
    'Anthropic 和 Gateway 会转换受支持的 OpenAI 字段。其他客户端字段仅在下方声明路径后才进入原生请求。',
  ],
  allSupport: [
    'These settings apply to each eligible connection. OpenAI-compatible requests preserve bounded extensions; Anthropic and Gateway require native extension paths to be declared.',
    '这些设置应用于每个符合条件的连接。OpenAI 兼容请求会保留有界扩展；Anthropic 和 Gateway 需要明确声明原生扩展路径。',
  ],
  inherited: ['Inherited from the charity model', '继承自公益模型'],
  sourceModel: ['Model setting', '模型设置'],
  sourceBinding: ['Connection setting', '连接设置'],
  revision: ['Configuration revision', '配置修订'],
  forwardHeaders: ['Client headers to forward', '透传的客户端请求头'],
  fixedHeaders: ['Fixed outbound headers', '固定出站请求头'],
  bodyDefaults: ['Body defaults (only when absent)', '请求主体缺省值（仅缺失时补入）'],
  bodyForced: ['Forced body values', '请求主体强制值'],
  nativePaths: ['Declared native extension paths', '已声明的原生扩展路径'],
  listHelp: [
    'One name or JSON Pointer per line. Duplicate or unsafe entries are rejected.',
    '每行一个名称或 JSON Pointer；重复或不安全的条目会被拒绝。',
  ],
  bodyHelp: [
    'Use an RFC 6901 JSON Pointer for an object field. Enter a valid JSON value, including quotes for strings.',
    '使用 RFC 6901 JSON Pointer 指向对象字段；请输入合法 JSON，字符串需要引号。',
  ],
  fixedHelp: [
    'Fixed values take priority over forwarded client values. Existing values remain hidden.',
    '固定值优先于客户端透传值；已有值不会再次显示。',
  ],
  mode: ['Configuration source', '配置来源'],
  replace: ['Set here', '在此设置'],
  inherit: ['Inherit model', '继承模型'],
  name: ['Header or path', '请求头或路径'],
  value: ['Value', '值'],
  action: ['Edit', '编辑'],
  keep: ['Keep saved value', '保留已存值'],
  change: ['Replace value', '替换值'],
  clear: ['Remove value', '移除值'],
  add: ['Add field', '添加字段'],
  empty: ['No entries configured.', '尚未配置条目。'],
  masked: ['Saved value is hidden', '已存值已隐藏'],
  effective: ['Effective setting', '实际生效设置'],
  save: ['Save request adaptation', '保存请求适配'],
  saved: ['Request adaptation saved.', '请求适配已保存。'],
  loading: ['Loading request adaptation…', '正在加载请求适配…'],
  loadError: ['Could not load request adaptation.', '无法加载请求适配。'],
  stale: [
    'The displayed settings could not be refreshed and may be stale. Refresh successfully before saving.',
    '显示的设置未能刷新，可能已过期。请先成功刷新，再保存。',
  ],
  saveError: [
    'Could not save request adaptation. Check the fields and retry after refreshing.',
    '无法保存请求适配。请检查字段，刷新后重试。',
  ],
  unknown: [
    'The save outcome is uncertain. Refresh before trying again.',
    '保存结果暂不确定，请先刷新后再重试。',
  ],
  conflict: [
    'The configuration changed. Refresh before editing again.',
    '配置已发生变化，请刷新后重新编辑。',
  ],
  invalidJSON: [
    'Enter a valid JSON value for each changed body field.',
    '请为每个替换的主体字段输入合法 JSON 值。',
  ],
  duplicate: [
    'Names and paths must be unique within each section.',
    '同一分区中的名称或路径不可重复。',
  ],
  refresh: ['Refresh configuration', '刷新配置'],
  readOnly: ['This setting is read-only for your role.', '当前角色只能查看此配置。'],
} as const;

export type RequestAdaptationCopyKey = keyof typeof copy;

export function useRequestAdaptationCopy() {
  const { i18n } = useTranslation();
  const chinese = (i18n.resolvedLanguage ?? i18n.language).toLowerCase().startsWith('zh');
  const copyForLanguage = useCallback(
    (key: RequestAdaptationCopyKey) => copy[key][chinese ? 1 : 0],
    [chinese],
  );
  return copyForLanguage;
}
