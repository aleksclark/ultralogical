/**
 * Saved views — localStorage per browser (pragmatic shortcut; server-side in later phase).
 */
import type { QueryFilter, QuerySort } from "@/query/types";

export type SavedView = {
  id: string;
  name: string;
  collection: string;
  q: string;
  filters: QueryFilter[];
  sorts: QuerySort[];
  limit: number;
  columns: string[];
  tenantId: string;
  createdAt: string;
};

const KEY = "uc_admin_saved_views_v1";

function readAll(): SavedView[] {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as SavedView[];
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function writeAll(views: SavedView[]) {
  try {
    localStorage.setItem(KEY, JSON.stringify(views));
  } catch {
    /* quota / private */
  }
}

export function listSavedViews(collection: string): SavedView[] {
  return readAll().filter((v) => v.collection === collection);
}

export function getSavedView(id: string): SavedView | undefined {
  return readAll().find((v) => v.id === id);
}

export function saveView(view: Omit<SavedView, "id" | "createdAt"> & { id?: string }): SavedView {
  const all = readAll();
  const next: SavedView = {
    ...view,
    id: view.id || `view_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 7)}`,
    createdAt: new Date().toISOString(),
  };
  const idx = all.findIndex((v) => v.id === next.id);
  if (idx >= 0) all[idx] = next;
  else all.push(next);
  writeAll(all);
  return next;
}

export function deleteSavedView(id: string) {
  writeAll(readAll().filter((v) => v.id !== id));
}
