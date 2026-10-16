type CaptureMfaRequest = () => (() => void) | undefined;
const observers = new Set<CaptureMfaRequest>();

/** Capture the current account before a request, then notify only that account. */
export function captureMfaRequiredHandlers(): (() => void)[] {
  return [...observers].flatMap((capture) => {
    const notify = capture();
    return notify ? [notify] : [];
  });
}

export function observeMfaRequired(capture: CaptureMfaRequest): () => void {
  observers.add(capture);
  return () => {
    observers.delete(capture);
  };
}
