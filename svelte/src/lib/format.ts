// Formatting helpers shared by container views (parity with legacy UI).
import type { PortSummary } from './types/api';

// Renders a single Docker port the way the legacy containers page did:
// "PublicPort:PrivatePort/Type" when published, "PrivatePort/Type" otherwise.
export function formatPort(p: PortSummary): string {
  return p.PublicPort
    ? `${p.PublicPort}:${p.PrivatePort}/${p.Type}`
    : `${p.PrivatePort}/${p.Type}`;
}

// Renders the full port list as a comma-separated string ("32768:48080/tcp, 8080/tcp").
export function formatPorts(ports: PortSummary[] | undefined | null): string {
  if (!Array.isArray(ports) || ports.length === 0) return '—';
  return ports.map(formatPort).join(', ');
}
