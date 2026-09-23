// Utilities for Dockge-style compose stack editing.
// Ports of dockge-mod common/util-common.ts helpers.

export interface ParsedPort {
  url: string;
  display: string;
}

/**
 * Parse a docker port mapping into a clickable URL + display string.
 * Handles: "3000", "3000-3005", "8000:8000", "127.0.0.1:8001:8001",
 * "0.0.0.0:8080->8080/tcp" (docker ps format), "/udp" suffixes.
 * Port 443 → https, other tcp → http.
 */
export function parseDockerPort(input: string, hostname: string): ParsedPort {
  let port: string;
  let display: string;

  const parts = input.split('/');
  let part1 = parts[0];
  let protocol = parts[1] || 'tcp';

  // docker ps format: split host part before "->"
  const arrow = part1.indexOf('->');
  if (arrow >= 0) {
    part1 = part1.split('->')[0];
    const colon = part1.indexOf(':');
    if (colon >= 0) {
      part1 = part1.split(':')[1];
    }
  }

  const lastColon = part1.lastIndexOf(':');

  if (lastColon === -1) {
    // Just a port or port range
    const dash = part1.indexOf('-');
    if (dash === -1) {
      port = part1;
    } else {
      port = part1.substring(0, dash);
    }
    display = part1;
  } else {
    // Port mapping
    let hostPart = part1.substring(0, lastColon);
    display = hostPart;

    const dash = part1.indexOf('-');
    if (dash !== -1) {
      hostPart = part1.substring(0, dash);
    }

    const colon = hostPart.indexOf(':');
    if (colon !== -1) {
      hostname = hostPart.substring(0, colon);
      port = hostPart.substring(colon + 1);
    } else {
      port = hostPart;
    }
  }

  const portInt = parseInt(port, 10);

  if (portInt === 443) {
    protocol = 'https';
  } else if (protocol === 'tcp') {
    protocol = 'http';
  }

  return {
    url: `${protocol}://${hostname}:${portInt}`,
    display
  };
}

/** Parse .env content (KEY=VALUE lines, # comments) into a map. */
export function parseEnvFile(content: string): Record<string, string> {
  const env: Record<string, string> = {};
  for (const rawLine of content.split('\n')) {
    const line = rawLine.trim();
    if (!line || line.startsWith('#')) continue;
    const eq = line.indexOf('=');
    if (eq === -1) continue;
    const key = line.substring(0, eq).trim();
    let value = line.substring(eq + 1);
    // Strip matching surrounding quotes (dotenv semantics)
    if (
      (value.startsWith('"') && value.endsWith('"')) ||
      (value.startsWith("'") && value.endsWith("'"))
    ) {
      value = value.substring(1, value.length - 1);
    }
    if (key) env[key] = value;
  }
  return env;
}

/** Stack status → tailwind badge classes. */
export function stackStatusColor(status: string): string {
  switch (status) {
    case 'running':
      return 'bg-emerald-600 text-white';
    case 'exited':
      return 'bg-red-600 text-white';
    case 'draft':
      return 'bg-zinc-600 text-white';
    case 'partial':
      return 'bg-amber-600 text-white';
    default:
      return 'bg-zinc-500 text-white';
  }
}

/** Container/service state → tailwind badge classes for ContainerCard component. */
export function serviceStateColor(state?: string, health?: string): string {
  if (health === 'healthy') return 'bg-emerald-600 text-white';
  if (health === 'unhealthy') return 'bg-red-600 text-white';
  
  if (!state) return 'bg-zinc-500 text-white';
  
  const s = state.toLowerCase();
  if (s === 'running' || s === 'starting' || s === 'restarting') {
    return 'bg-emerald-600 text-white';
  }
  if (s === 'exited' || s === 'dead' || s === 'stopped') {
    return 'bg-red-600 text-white';
  }
  if (s === 'paused') {
    return 'bg-amber-600 text-white';
  }
  if (s === 'created' || s === 'configured') {
    return 'bg-zinc-600 text-white';
  }
  
	return 'bg-zinc-500 text-white';
}

export const RESTART_POLICIES = ['always', 'unless-stopped', 'on-failure', 'no'] as const;

