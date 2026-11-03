import { useId, type ReactNode, type ComponentPropsWithRef } from 'react';
import { Tabs as AriaTabs, TabList, Tab } from 'react-aria-components';

export function Tabs<V extends string>({
  label,
  value,
  tabs,
  onChange,
  children,
  className,
  panelId: externalPanelId,
}: {
  label: string;
  children?: ReactNode;
  className?: string;
  panelId?: string;
  value: V;
  tabs: readonly { value: V; label: ReactNode; count?: number; id?: string; panelId?: string }[];
  onChange: (next: V) => void;
}) {
  const id = useId();
  const active = tabs.find((tab) => tab.value === value);
  const panelId = externalPanelId ?? (children !== undefined ? id + '-panel' : undefined);
  return (
    <>
      <AriaTabs
        className="nb-tabs-root"
        selectedKey={value}
        onSelectionChange={(key) => onChange(String(key) as V)}
      >
        <TabList className={className ?? 'nb-tabs'} aria-label={label}>
          {tabs.map((tab) => (
            <Tab
              key={tab.value}
              id={tab.value}
              render={(props) => (
                <div
                  {...(props as ComponentPropsWithRef<'div'>)}
                  id={tab.id ?? id + '-' + tab.value}
                  aria-controls={tab.panelId ?? panelId}
                />
              )}
            >
              {tab.label}
              {tab.count !== undefined ? <span className="nb-count">{tab.count}</span> : null}
            </Tab>
          ))}
        </TabList>
      </AriaTabs>
      {children !== undefined ? (
        <div
          id={panelId}
          role="tabpanel"
          aria-labelledby={active?.id ?? id + '-' + value}
          className="nb-tab-content"
          tabIndex={0}
        >
          {children}
        </div>
      ) : null}
    </>
  );
}
