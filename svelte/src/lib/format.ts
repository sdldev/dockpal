// Formatting helpers shared by container views (parity with legacy UI).
import type { PortSummary } from './types/api';

// Renders a single Docker port the way the legacy containers page did:
// "PublicPort:PrivatePort/Type" when published, "PrivatePort/Type" otherwise.
export function formatPort(p: PortSummary): string {
  return p.PublicPort
    ? `${p.PublicPort}:${p.PrivatePort}/${p.Type}`
    : `${p.PrivatePort}/${p.Type}`;
}

// Dedupes ports by their rendered representation. Docker reports each
// published port once per binding (IPv4 0.0.0.0 and IPv6 ::) and formatPort
// drops the IP, so those entries render identically.
export function dedupePorts(ports: PortSummary[]): PortSummary[] {
  const seen = new Set<string>();
  const out: PortSummary[] = [];
  for (const p of ports) {
    const s = formatPort(p);
    if (!seen.has(s)) {
      seen.add(s);
      out.push(p);
    }
  }
  return out;
}

// Renders the full port list as a comma-separated string ("32768:48080/tcp, 8080/tcp").
export function formatPorts(ports: PortSummary[] | undefined | null): string {
  if (!Array.isArray(ports) || ports.length === 0) return '—';
  return dedupePorts(ports).map(formatPort).join(', ');
}
