import { useTranslation } from 'react-i18next';
import type { ParameterKey, TaskStatus } from './publicTypes';
export function usePictureBookText() {
  const { i18n } = useTranslation();
  return (zh: string, en: string) => (i18n.resolvedLanguage?.startsWith('zh') ? zh : en);
}
export type PictureBookText = ReturnType<typeof usePictureBookText>;
export function taskStatusLabel(status: TaskStatus, t: PictureBookText) {
  return {
    queued: t('排队中', 'Queued'),
    dispatching: t('正在开始', 'Starting'),
    running: t('生成中', 'Generating'),
    succeeded: t('已生成', 'Generated'),
    failed: t('生成失败', 'Failed'),
    cancelled: t('已撤回', 'Cancelled'),
    unknown_refunded: t('结果未知，已退款', 'Unknown result, refunded'),
  }[status];
}
export function parameterLabel(key: ParameterKey, t: PictureBookText) {
  return {
    prompt: t('提示词', 'Prompt'),
    negative_prompt: t('反向提示词', 'Negative prompt'),
    n: t('生成张数', 'Image count'),
    size: t('尺寸', 'Size'),
    aspect_ratio: t('宽高比', 'Aspect ratio'),
    resolution: t('分辨率', 'Resolution'),
    seed: t('随机种子', 'Seed'),
    steps: t('步数', 'Steps'),
    guidance: t('引导强度', 'Guidance'),
    quality: t('质量', 'Quality'),
  }[key];
}
export function taskErrorLabel(code: string | null, t: PictureBookText) {
  const labels: Record<string, string> = {
    upstream_failed: t(
      '生成服务未能完成请求。',
      'The generation service could not complete the request.',
    ),
    invalid_result: t('未取得有效图片。', 'No valid image was received.'),
    response_too_large: t('生成结果超过大小限制。', 'The result exceeded the size limit.'),
    execution_timeout: t('生成超时。', 'Generation timed out.'),
    result_unknown: t(
      '无法确认生成结果，已退款。',
      'The result could not be confirmed; the charge was refunded.',
    ),
    queue_timeout: t('排队超时，已退款。', 'The queue wait expired; the charge was refunded.'),
    cancelled_by_user: t('已撤回并退款。', 'Cancelled and refunded.'),
    activity_paused: t(
      '活动暂停，未开始的任务已退款。',
      'The activity was paused; queued tasks were refunded.',
    ),
    maintenance: t(
      '站点维护，未开始的任务已退款。',
      'Site maintenance cancelled and refunded queued tasks.',
    ),
    model_unavailable: t(
      '所选模型已下架，未开始的任务已退款。',
      'The model was removed; queued tasks were refunded.',
    ),
    account_restricted: t(
      '账号状态已变化，任务已终止。',
      'The task ended because the account status changed.',
    ),
    service_restarted: t(
      '服务重启，未开始的任务已退款。',
      'The service restarted; queued tasks were refunded.',
    ),
  };
  return code === null
    ? ''
    : (labels[code] ??
        t(
          '任务未能完成，请查看收费与退款状态。',
          'The task could not finish. Check its charge and refund status.',
        ));
}
