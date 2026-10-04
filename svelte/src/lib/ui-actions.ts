// Shared destructive-action flow. Single home for the delete-with-confirm
// bodies that used to be copied across tabs and pages.
import { api } from './api/client';
import { addToast } from './store';

export interface DeleteOptions {
  /** Called after a successful delete (reload the list, filter locally…). */
  refresh?: () => unknown | Promise<unknown>;
  /** Error toast when the thrown error is not an Error (default "Delete failed"). */
  errorFallback?: string;
}

/**
 * DELETE the URL, toast the outcome, then run refresh. Never rejects — the
 * user already sees the error toast, so callers can skip try/catch and clear
 * their pending/busy state right after the await. Returns true on success.
 */
export async function deleteThenToast(
  url: string,
  successMessage: string,
  opts: DeleteOptions = {}
): Promise<boolean> {
  try {
    await api.delete(url);
    addToast(successMessage, 'success');
    await opts.refresh?.();
    return true;
  } catch (e) {
    addToast(e instanceof Error ? e.message : (opts.errorFallback ?? 'Delete failed'), 'error');
    return false;
  }
}
