import { usePictureBookText } from '@shared/picturebook/copy';
export function ImagePolicyNotice() {
  const t = usePictureBookText();
  return (
    <aside
      className="picturebook-notice"
      aria-label={t('生成与收费说明', 'Generation and billing')}
    >
      <p>
        {t(
          '提交时按张数预扣草稿纸和画笔。只要至少生成一张有效图片，就收取该任务的全部费用；全部失败或开始前撤回则全额退款。',
          'Submission reserves sketch paper and brushes for the requested image count. Any valid generated image makes the full task charge payable. Complete failure or cancellation before generation receives a full refund.',
        )}
      </p>
      <p>
        {t(
          '关闭网页不会取消任务。完成后有10分钟可领取图片，请及时下载原图。站点不长期保存图片；领取窗口结束或服务重启可能造成图片丢失，成功生成的费用不予退还。',
          'Closing this page does not cancel the task. Images can be collected for 10 minutes after completion; download the originals promptly. Images are not stored long term and may be lost after that window or a service restart. Successful generation is not refunded.',
        )}
      </p>
      <p>
        {t(
          '任务按先后顺序处理，排队位置不代表预计完成时间。只有尚未开始的任务可以撤回。',
          'Tasks are processed in arrival order. Queue position is not an estimated completion time. Only tasks that have not started can be cancelled.',
        )}
      </p>
    </aside>
  );
}
