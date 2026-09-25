import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { readLoginRestrictions, type AutomaticRestriction } from '@shared/operations/restrictions';
import { useDateTimeFormatter } from '@shared/utils/datetime';

export function AutomaticRestrictions({ restrictions }: { restrictions: AutomaticRestriction[] }) {
  const formatDateTime = useDateTimeFormatter();
  const { i18n } = useTranslation();
  const en = i18n.language.startsWith('en');
  const [now, setNow] = useState(() => Math.floor(Date.now() / 1000));
  useEffect(() => {
    if (!restrictions.length) return;
    const timer = window.setInterval(() => setNow(Math.floor(Date.now() / 1000)), 1000);
    return () => window.clearInterval(timer);
  }, [restrictions.length]);
  const current = restrictions.filter((r) => r.ends_at === null || r.ends_at > now);
  if (!current.length) return null;
  return (
    <section
      className="nb-card"
      aria-label={en ? 'Current automatic restrictions' : '当前自动限制'}
    >
      <h2>{en ? 'Current automatic restrictions' : '当前自动限制'}</h2>
      {current.map((r) => (
        <div key={r.kind}>
          <strong>
            {r.kind === 'ban'
              ? en
                ? 'Account access restricted'
                : '账号访问受限'
              : en
                ? 'Charity calls suspended'
                : '公益调用已暂停'}
          </strong>
          <p>
            {r.reason_code === 'charity_rpm'
              ? en
                ? 'Repeated charity requests exceeded the rate limit.'
                : '公益调用多次超过请求频率限制。'
              : en
                ? 'Charity request content did not meet the minimum length.'
                : '公益调用的有效内容未达到最低长度要求。'}
          </p>
          <p>
            {en ? 'Ends: ' : '结束时间：'}
            {r.ends_at === null
              ? en
                ? 'No scheduled end'
                : '未设定结束时间'
              : formatDateTime(r.ends_at, en ? 'en' : 'zh')}
          </p>
        </div>
      ))}
    </section>
  );
}

// The fragment is untrusted display data, never a session or an authorization
// input. Remove it from browser history after this one page receives it.
export function LoginRestrictions() {
  const [restrictions] = useState(() =>
    window.location.pathname === '/access-denied'
      ? readLoginRestrictions(window.location.hash)
      : [],
  );
  useEffect(() => {
    if (
      window.location.pathname === '/access-denied' &&
      window.location.hash.startsWith('#restrictions=')
    ) {
      window.history.replaceState(
        window.history.state,
        '',
        window.location.pathname + window.location.search,
      );
    }
  }, []);
  return <AutomaticRestrictions restrictions={restrictions} />;
}
