import { useState } from 'react';
import { useTranslation } from 'react-i18next';

function display(value: unknown): string {
  if (typeof value === 'string') return value;
  return JSON.stringify(value, null, 2) ?? String(value);
}
export function MessageField({ name, value }: { name: string; value: unknown }) {
  const { t } = useTranslation();
  const text = display(value);
  const long = text.length > 2000 || text.split('\n').length > 20;
  return (
    <div className="nb-message-field">
      <dt>{name}</dt>
      <dd>
        {long ? (
          <details>
            <summary>{t('common.messages.expand')}</summary>
            <pre>{text}</pre>
          </details>
        ) : (
          <pre>{text}</pre>
        )}
      </dd>
    </div>
  );
}

/** Each message keeps its position and every original field. Values are text sinks. */
export function MessagesView({ messages }: { messages: readonly unknown[] }) {
  const { t } = useTranslation();
  const [page, setPage] = useState(1);
  const pages = Math.max(1, Math.ceil(messages.length / 20));
  const shownPage = Math.min(page, pages);
  const start = (shownPage - 1) * 20;
  return (
    <section className="nb-messages" aria-label={t('common.messages.title')}>
      {messages.length === 0 ? <p>{t('common.messages.empty')}</p> : null}
      <ol start={start + 1}>
        {messages.slice(start, start + 20).map((message, index) => {
          const object =
            message !== null && typeof message === 'object' && !Array.isArray(message)
              ? (message as Record<string, unknown>)
              : null;
          return (
            <li key={start + index}>
              <h3>
                {t('common.messages.number', { number: start + index + 1 })}
                {typeof object?.role === 'string' ? <> · {object.role}</> : null}
              </h3>
              {object ? (
                <dl>
                  {Object.entries(object).map(([name, value]) => (
                    <MessageField key={name} name={name} value={value} />
                  ))}
                </dl>
              ) : (
                <details>
                  <summary>{t('common.messages.original')}</summary>
                  <pre>{display(message)}</pre>
                </details>
              )}
            </li>
          );
        })}
      </ol>
      {pages > 1 ? (
        <nav className="nb-message-pagination" aria-label={t('common.pagination')}>
          <button
            type="button"
            className="btn btn-secondary"
            disabled={shownPage === 1}
            onClick={() => setPage(shownPage - 1)}
          >
            {t('common.previous')}
          </button>
          <span>{t('common.page', { page: shownPage })}</span>
          <button
            type="button"
            className="btn btn-secondary"
            disabled={shownPage === pages}
            onClick={() => setPage(shownPage + 1)}
          >
            {t('common.next')}
          </button>
        </nav>
      ) : null}
    </section>
  );
}
