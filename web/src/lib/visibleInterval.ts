// setInterval for network polls, minus the hidden tabs. One forgotten tab
// polling every 10 s was 8,640 requests a day against whatever metered front
// the site sits behind. No imports, so node --test loads it as it is.

/** Runs fn every ms while the tab is visible, and once when it becomes visible
 *  again; a hidden tab sends nothing. Returns the cleanup. The caller makes its
 *  own first call. */
export function visibleInterval(fn: () => void, ms: number): () => void {
  const run = () => {
    if (!document.hidden) fn();
  };
  const t = setInterval(run, ms);
  document.addEventListener("visibilitychange", run);
  return () => {
    clearInterval(t);
    document.removeEventListener("visibilitychange", run);
  };
}
