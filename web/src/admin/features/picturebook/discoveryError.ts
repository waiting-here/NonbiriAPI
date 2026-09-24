import type { PictureBookText } from '@shared/picturebook/copy';

export function discoveryError(
  code: string | null,
  status: number | undefined,
  t: PictureBookText,
) {
  if (status === 401 || status === 403)
    return t(
      '服务拒绝了访问。请检查活动专用密钥是否有效，以及是否有读取模型目录的权限。',
      'The service refused access. Check the activity key and its permission to list models.',
    );
  if (status === 404)
    return t(
      '模型目录地址不存在。请检查服务地址和适配配置中的 discovery.path；该服务也可能不提供模型列表接口。',
      'The model catalog endpoint was not found. Check the service URL and discovery.path; the provider may not offer model discovery.',
    );
  if (status === 429)
    return t(
      '服务限制了请求频率。请稍后重试，并检查服务端限额。',
      'The service rate-limited the request. Wait before retrying and check its quota.',
    );
  if (status && status >= 300 && status < 400)
    return t(
      '模型目录返回了重定向。请填写最终接口地址，系统不会自动跟随跳转。',
      'The catalog returned a redirect. Configure the final endpoint; redirects are not followed.',
    );
  if (status && status >= 500)
    return t(
      '图片服务暂时出错。请检查服务状态，稍后重新拉取。',
      'The image service returned a server error. Check its status and retry later.',
    );
  if (code === 'invalid_result')
    return t(
      '未取得有效的模型目录。请核对返回的是 JSON 模型列表，以及 discovery.items_pointer 和 id_pointer 对应的字段；目录最多 1,000 个模型，ID 不能重复。若未收到 HTTP 响应，也请检查服务连通性。',
      'No valid model catalog was received. Check the JSON list and discovery.items_pointer / id_pointer; at most 1,000 unique model IDs are allowed. If no HTTP response was received, also check connectivity.',
    );
  if (code === 'response_too_large')
    return t(
      '模型目录响应超过 1 MiB 上限。请使用返回较小目录的接口。',
      'The model catalog exceeded the 1 MiB response limit. Use an endpoint with a smaller catalog.',
    );
  if (code === 'execution_timeout')
    return t(
      '排队或拉取模型目录超时。请检查服务连通性、RPM 和并发配置后重试。',
      'Waiting for or fetching the catalog timed out. Check connectivity, RPM and concurrency settings, then retry.',
    );
  if (code === 'service_restarted')
    return t(
      '拉取期间服务重启。请重新拉取模型目录。',
      'The service restarted during discovery. Start a new catalog refresh.',
    );
  return t(
    '模型目录请求未成功。请检查服务地址、活动专用密钥和模型目录接口后重试。',
    'Model discovery failed. Check the service URL, activity key and catalog endpoint, then retry.',
  );
}
