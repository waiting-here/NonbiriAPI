import { SafeCopyValue } from './components';
import { usePersonalAutomationCopy } from './personalAutomationCopy';
import { personalAutomationExamples as examples } from './personalAutomationExamples';
import './personalAutomationGuide.css';

const routes = [
  ['endpoints', '/api/automation/endpoints'],
  ['endpoint', '/api/automation/endpoints/{id}'],
  ['keys', '/api/automation/endpoints/{id}/keys'],
  ['key', '/api/automation/endpoints/{id}/keys/{keyId}'],
  ['models', '/api/automation/models'],
  ['model', '/api/automation/models/{id}'],
  ['bindings', '/api/automation/models/{id}/bindings'],
] as const;

export function PersonalAutomationGuide() {
  const { t: text } = usePersonalAutomationCopy();
  const example = (key: keyof typeof examples) => (
    <div>
      <h4>{text(key === 'read' ? 'readExample' : key)}</h4>
      <SafeCopyValue value={examples[key]} label={text(key === 'read' ? 'readExample' : key)} />
    </div>
  );
  return (
    <details className="core-card personal-automation-guide">
      <summary>
        <strong>{text('title')}</strong>
      </summary>
      <div className="core-stack">
        <p>{text('intro')}</p>
        <section>
          <h3>{text('identityTitle')}</h3>
          <p>{text('identity')}</p>
          <p>{text('scope')}</p>
        </section>
        <section>
          <h3>{text('lookupTitle')}</h3>
          <p>{text('lookup')}</p>
          <table>
            <thead>
              <tr>
                <th>{text('method')}</th>
                <th>{text('path')}</th>
                <th>{text('purpose')}</th>
              </tr>
            </thead>
            <tbody>
              {routes.map(([key, path]) => (
                <tr key={key}>
                  <td>GET</td>
                  <td>
                    <code>{path}</code>
                  </td>
                  <td>{text(key)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <p>{text('pagination')}</p>
          {example('read')}
        </section>
        <section>
          <h3>{text('importTitle')}</h3>
          <p>{text('import')}</p>
          {example('importBody')}
          {example('importRequest')}
        </section>
        <section>
          <h3>{text('bindTitle')}</h3>
          <p>{text('bind')}</p>
          <p>{text('catalog')}</p>
          {example('bindBody')}
          {example('bindRequest')}
        </section>
        <section>
          <h3>{text('resultTitle')}</h3>
          <p>{text('result')}</p>
          {example('resultExample')}
          <p>{text('retry')}</p>
          <p>{text('statuses')}</p>
          <p>{text('limits')}</p>
        </section>
      </div>
    </details>
  );
}
