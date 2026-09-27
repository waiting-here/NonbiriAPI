import { useEffect } from 'react';
import { useBlocker } from 'react-router';

export function useDraftGuard(dirty: boolean): (message: string) => boolean {
  const blocker = useBlocker(dirty);
  useEffect(() => {
    if (blocker.state !== 'blocked') return;
    if (window.confirm('Discard unsaved Fat Fish changes? / 放弃未保存的大肥鱼编辑？')) blocker.proceed();
    else blocker.reset();
  }, [blocker]);
  useEffect(() => {
    if (!dirty) return;
    const onBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', onBeforeUnload);
    return () => window.removeEventListener('beforeunload', onBeforeUnload);
  }, [dirty]);
  return (message) => !dirty || window.confirm(message);
}
