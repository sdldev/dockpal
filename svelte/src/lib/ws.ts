// Shared WebSocket URL helpers. Every browser WS endpoint takes its
// credential via ?token= — preferably a single-use 60s ticket, with the raw
// JWT as the fallback for older backends (audit-auth L1).
import { api, getToken } from './api/client';

/** ws(s):// origin of the panel, matching the page's protocol. */
export function wsOrigin(): string {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${proto}//${location.host}`;
}

/**
 * Credential for a ?token= WS URL: a single-use ticket when the backend has
 * /ws-ticket, else the JWT. Unlike getWSTicket() this never rejects, so log
 * viewers that must work against older backends can always open a stream.
 */
export async function wsCredential(): Promise<string> {
  try {
    const res = await api.get<{ ticket: string }>('/ws-ticket');
    return res.ticket;
  } catch {
    // Older backend without /ws-ticket — fall back to the JWT.
    return getToken() ?? '';
  }
}

/** Builds an instance-scoped WS URL with the ?token= credential attached. */
export function instanceWSURL(instanceId: string, pathAndQuery: string, credential: string): string {
  const sep = pathAndQuery.includes('?') ? '&' : '?';
  return `${wsOrigin()}/api/instances/${encodeURIComponent(instanceId)}${pathAndQuery}${sep}token=${encodeURIComponent(credential)}`;
}
