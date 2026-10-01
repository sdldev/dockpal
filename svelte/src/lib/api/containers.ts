/** Instance-aware base paths for container/image endpoints.
 *
 * Every page that reads or mutates containers/images must route through these
 * helpers so the selected instance in the sidebar is honored — calling the
 * bare `/containers` path always hits the local host (audit-stack-container C2).
 */

import { api } from './client';

/** Base path for container endpoints on the given instance. */
export function containersBasePath(instanceId: string): string {
  return instanceId === 'local' || !instanceId
    ? '/containers'
    : `/instances/${encodeURIComponent(instanceId)}/containers`;
}

/** Base path for image endpoints on the given instance. */
export function imagesBasePath(instanceId: string): string {
  return instanceId === 'local' || !instanceId
    ? '/images'
    : `/instances/${encodeURIComponent(instanceId)}/images`;
}

/**
 * Fetch a single-use, 60s WS ticket. Prefer tickets over raw JWTs in
 * WebSocket URLs so long-lived tokens never appear in URLs / proxy logs
 * (audit-auth L1). Falls back to the JWT only if the ticket request fails
 * (older backend) — callers should just always await this.
 */
export async function getWSTicket(): Promise<string> {
  const res = await api.get<{ ticket: string }>('/ws-ticket');
  return res.ticket;
}
