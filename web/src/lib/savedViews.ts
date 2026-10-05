// Saved per-page view states (filters, sorts, toggles) — localStorage only,
// one key per page under the "sd-views:" prefix, same try/catch-safe pattern
// as the sd-purpose-hidden store. Pure module: no React, no DOM beyond
// localStorage, SSR-safe (returns [] on the server).

export interface SavedView {
  name: string;
  state: Record<string, unknown>;
}

const PREFIX = "sd-views:";

function storageKey(pageKey: string): string {
  return PREFIX + pageKey;
}

function isSavedView(v: unknown): v is SavedView {
  if (typeof v !== "object" || v === null) return false;
  const o = v as { name?: unknown; state?: unknown };
  return (
    typeof o.name === "string" &&
    typeof o.state === "object" &&
    o.state !== null &&
    !Array.isArray(o.state)
  );
}

export function getViews(pageKey: string): SavedView[] {
  if (typeof window === "undefined") return [];
  try {
    const v: unknown = JSON.parse(localStorage.getItem(storageKey(pageKey)) ?? "[]");
    return Array.isArray(v) ? v.filter(isSavedView) : [];
  } catch {
    return [];
  }
}

function writeViews(pageKey: string, views: SavedView[]) {
  try {
    localStorage.setItem(storageKey(pageKey), JSON.stringify(views));
  } catch {
    /* storage blocked — the in-memory list still works for this render */
  }
}

/** Saving under an existing name replaces that view (rename-free overwrite). */
export function saveView(pageKey: string, name: string, state: Record<string, unknown>): void {
  const views = getViews(pageKey).filter((v) => v.name !== name);
  views.push({ name, state });
  writeViews(pageKey, views);
  emit(pageKey);
}

export function deleteView(pageKey: string, name: string): void {
  writeViews(pageKey, getViews(pageKey).filter((v) => v.name !== name));
  emit(pageKey);
}

// ── reactive layer for React consumers (useSyncExternalStore) ────────────
// Snapshots are cached per pageKey so the array identity is stable between
// writes; saveView/deleteView invalidate and notify.

const listeners = new Set<() => void>();
const snapshotCache = new Map<string, SavedView[]>();
const NO_VIEWS: SavedView[] = [];

function emit(pageKey: string): void {
  snapshotCache.delete(pageKey);
  for (const l of listeners) l();
}

export function subscribeViews(cb: () => void): () => void {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

/** Cached read for useSyncExternalStore — same array back until a write. */
export function getViewsSnapshot(pageKey: string): SavedView[] {
  let v = snapshotCache.get(pageKey);
  if (v === undefined) {
    v = getViews(pageKey);
    snapshotCache.set(pageKey, v);
  }
  return v;
}

/** SSR snapshot — the server never has saved views. */
export function getServerViewsSnapshot(): SavedView[] {
  return NO_VIEWS;
}
