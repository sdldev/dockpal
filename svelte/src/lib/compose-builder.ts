// Builds a Docker Compose YAML document from the "Custom Install" form.
// The output is compatible with dockpal's compose-native deploy pipeline
// (POST /instances/:id/deploy/stream), which handles env substitution,
// restart-policy normalization, and log streaming.
export interface CustomPortRow {
  host: number;
  container: number;
  protocol: 'tcp' | 'udp';
}

export interface CustomEnvRow {
  key: string;
  value: string;
}

export interface CustomVolumeRow {
  host: string;
  container: string;
}

export interface CustomInstallInput {
  serviceName: string;
  image: string;
  ports: CustomPortRow[];
  env: CustomEnvRow[];
  volumes: CustomVolumeRow[];
  networkMode: 'bridge' | 'host' | 'none' | 'custom';
  customNetwork: string;
}

function quoteScalar(value: string): string {
  if (/^(true|false|yes|no|null|\d+)$/i.test(value) || value.startsWith('${')) {
    return value;
  }
  if (/[:#\[\]{},&*!|>'"]|\s/.test(value)) {
    return `"${value.replace(/"/g, '\\"')}"`;
  }
  return value;
}

export function buildCustomCompose(input: CustomInstallInput): string {
  const lines: string[] = ['services:'];
  lines.push(`  ${input.serviceName}:`);
  lines.push(`    image: ${input.image}`);

  if (input.env.length > 0) {
    lines.push('    environment:');
    for (const row of input.env) {
      if (!row.key.trim()) continue;
      lines.push(`      ${row.key.trim()}: ${quoteScalar(row.value)}`);
    }
  }

  // Host networking ignores port bindings; omit them so Compose does not
  // warn or fail on the conflict.
  const hostNetwork = input.networkMode === 'host';
  const ports = input.ports.filter((p) => p.host > 0 && p.container > 0);
  if (ports.length > 0 && !hostNetwork) {
    lines.push('    ports:');
    for (const p of ports) {
      const proto = p.protocol === 'udp' ? '/udp' : '';
      lines.push(`      - '${p.host}:${p.container}${proto}'`);
    }
  }

  if (input.volumes.length > 0) {
    lines.push('    volumes:');
    for (const row of input.volumes) {
      const host = row.host.trim();
      const container = row.container.trim();
      if (!host || !container) continue;
      // Bare container paths get an anonymous volume; absolute host paths
      // are bind mounts.
      lines.push(`      - ${host}:${container}`);
    }
  }

  if (hostNetwork) {
    lines.push('    network_mode: host');
  } else if (input.networkMode === 'none') {
    lines.push('    network_mode: none');
  } else if (input.networkMode === 'custom' && input.customNetwork.trim()) {
    lines.push(`    networks:`);
    lines.push(`      - ${input.customNetwork.trim()}`);
    lines.push('networks:');
    lines.push(`  ${input.customNetwork.trim()}:`);
    lines.push('    external: true');
  }

  return lines.join('\n');
}