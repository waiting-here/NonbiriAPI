import { useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { I18nProvider } from 'react-aria-components';
import { Sheet } from '../../src/shared/components/ui/Sheet';
import { NativeDialog } from '../../src/shared/components/NativeDialog';
import { ConfirmDialog } from '../../src/shared/components/ConfirmDialog';
import { MoreMenu } from '../../src/shared/components/ui/MoreMenu';
import { Tabs } from '../../src/shared/components/ui/Tabs';
import { Toggle } from '../../src/shared/components/ui/Toggle';
import { Segmented } from '../../src/shared/components/ui/Segmented';
import { Field } from '../../src/shared/components/ui/Field';
import { SuggestInput } from '../../src/shared/components/ui/SuggestInput';
import '../../src/user/i18n';
import '../../src/shared/styles/index.css';

function Controls() {
  const scene = useRef<HTMLDivElement>(null);
  const [sheet, setSheet] = useState(false);
  const [native, setNative] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [value, setValue] = useState('');
  const [locked, setLocked] = useState(false);
  const [enabled, setEnabled] = useState(false);
  const [mode, setMode] = useState('add');
  const [tab, setTab] = useState('settings');
  return (
    <I18nProvider locale="en-US">
      <div ref={scene} className="scene">
        <button onClick={() => setSheet(true)}>Edit service</button>
        <button>Background action</button>
        <Sheet
          open={sheet}
          keepMounted
          title="Service"
          closeLabel="Close service"
          onClose={() => setSheet(false)}
        >
          <Tabs
            label="Service sections"
            value={tab}
            onChange={setTab}
            tabs={[
              { value: 'settings', label: 'Settings' },
              { value: 'history', label: 'History' },
            ]}
          >
            <span>{tab === 'settings' ? 'Configuration' : 'Previous changes'}</span>
          </Tabs>
          <button
            onClick={async () => {
              await fetch('/__aria-test/lock');
              setLocked(true);
            }}
          >
            Load configuration
          </button>
          <button onClick={() => setLocked(false)}>Unlock configuration</button>
          <fieldset disabled={locked}>
            <Toggle label="Enabled" checked={enabled} onChange={setEnabled} disabled={locked} />
            <Segmented
              label="Tag operation"
              value={mode}
              onChange={setMode}
              disabled={locked}
              options={[
                { value: 'add', label: 'Add' },
                { value: 'clear', label: 'Clear' },
              ]}
            />
            <label>
              Draft
              <input />
            </label>
            <Field label="Model" help="Choose or enter a model">
              {(props) => (
                <SuggestInput
                  {...props}
                  value={value}
                  onChange={setValue}
                  suggestions={['alpha', 'beta']}
                  disabled={locked}
                />
              )}
            </Field>
          </fieldset>
          <button onClick={() => void scene.current?.requestFullscreen()}>Enter full screen</button>
          <button onClick={() => void document.exitFullscreen()}>Exit full screen</button>
          <button onClick={() => setNative(true)}>Open game dialog</button>
          {native ? (
            <NativeDialog
              aria-label="Game dialog"
              onDismiss={() => setNative(false)}
              className="fixture-native"
            >
              <h2>Game dialog</h2>
              <MoreMenu
                label="Game actions"
                items={[
                  { label: 'Remove item', onSelect: () => setConfirm(true) },
                  { label: 'Disabled action', disabled: true },
                  { label: 'Other action' },
                ]}
              />
              <button onClick={() => setNative(false)}>Close game</button>
              <ConfirmDialog
                open={confirm}
                title="Remove item?"
                description="Review this action."
                confirmLabel="Remove"
                cancelLabel="Cancel"
                onCancel={() => setConfirm(false)}
                onConfirm={() => setConfirm(false)}
              />
            </NativeDialog>
          ) : null}
        </Sheet>
      </div>
    </I18nProvider>
  );
}
createRoot(document.getElementById('root')!).render(<Controls />);
