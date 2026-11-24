import { Fold } from '@shared/components/ui';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
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
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  return (
    <div className="personal-automation-guide">
      <Fold title={t('user.core.keys.automationTitle')} open={open} onOpenChange={setOpen}>
        {open ? <AutomationInstructions /> : null}
      </Fold>
    </div>
  );
}

function AutomationInstructions() {
  const { t: text } = usePersonalAutomationCopy();
  const example = (key: keyof typeof examples) => (
    <div>
      <h4>{text(key === 'read' ? 'readExample' : key)}</h4>
      <SafeCopyValue value={examples[key]} label={text(key === 'read' ? 'readExample' : key)} />
    </div>
  );
  return (
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
        <div className="nb-table-wrap">
          <table className="nb-table">
            <thead>
              <tr>
                <th scope="col">{text('method')}</th>
                <th scope="col">{text('path')}</th>
                <th scope="col">{text('purpose')}</th>
              </tr>
            </thead>
            <tbody>
              {routes.map(([key, path]) => (
                <tr key={key}>
                  <td data-cell="meta" data-label={text('method')}>
                    GET
                  </td>
                  <td data-cell="title" data-label={text('path')}>
                    <code>{path}</code>
                  </td>
                  <td data-cell="meta" data-label={text('purpose')}>
                    {text(key)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
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
  );
}
