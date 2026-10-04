// Shared session teardown. Single home for the logout copies that used to
// live inline in App.svelte and SettingsPage.svelte.
import { api, clearToken } from './api/client';
import { currentUser } from './store';
import { navigate } from './router';

/**
 * Drop the local session and land on the servers page — no server call.
 * Used by the global 401 handler (the stored token is dead) and as the
 * second half of performLogout.
 */
export function clearSession(): void {
  clearToken();
  currentUser.set(null);
  navigate('servers', {}, true);
}

/**
 * Best-effort server-side revocation, then clear the local session. A
 * failing revoke is worth seeing in the console but must not block the
 * local sign-out.
 */
export function performLogout(): void {
  api.post('/logout').catch((e) => console.warn('logout request failed', e));
  clearSession();
}
