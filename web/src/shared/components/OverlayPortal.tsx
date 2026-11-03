import {
  createContext,
  useContext,
  useLayoutEffect,
  useState,
  type ReactNode,
  type RefObject,
} from 'react';
import { Overlay } from 'react-aria';

export const NativeOverlayContext = createContext<RefObject<HTMLDialogElement | null> | null>(null);

/** A stable portal node preserves drafts when fullscreen changes its parent. */
export function OverlayPortal({ children }: { children: ReactNode }) {
  const nativeDialog = useContext(NativeOverlayContext);
  const [host] = useState(() => {
    const node = document.createElement('div');
    node.dataset.nbOverlayHost = '';
    node.style.display = 'contents';
    return node;
  });
  useLayoutEffect(() => {
    const move = () => {
      const target = nativeDialog?.current ?? document.fullscreenElement ?? document.body;
      if (host.parentElement !== target) target.append(host);
    };
    move();
    document.addEventListener('fullscreenchange', move);
    return () => {
      document.removeEventListener('fullscreenchange', move);
      host.remove();
    };
  }, [host, nativeDialog]);
  return (
    <Overlay portalContainer={host} disableFocusManagement>
      {children}
    </Overlay>
  );
}
