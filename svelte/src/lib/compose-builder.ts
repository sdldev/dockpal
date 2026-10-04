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

// Mirrors internal/validator.ValidateContainerName so an invalid name is
// rejected in the UI before it reaches the backend.
export const SERVICE_NAME_RE = /^[a-zA-Z0-9][a-zA-Z0-9_.\-]*$/;

export interface DeployFormInput {
  mode: 'template' | 'custom';
  serviceName: string;
  template: { env_required?: string[]; ports?: Array<{ container_port: number }> } | null;
  env: Record<string, string>;
  ports: Record<string, number>;
  customImage: string;
  customEnv: CustomEnvRow[];
  customPorts: CustomPortRow[];
}

/** Recomputes every field error for the deploy wizard form; empty = valid. */
export function collectDeployErrors(f: DeployFormInput): Record<string, string> {
  const e: Record<string, string> = {};

  const name = f.serviceName.trim();
  if (!name) {
    e.serviceName = 'App name is required';
  } else if (name.length > 128) {
    e.serviceName = 'App name must be 128 characters or fewer';
  } else if (!SERVICE_NAME_RE.test(name)) {
    e.serviceName = 'App name must start with a letter or digit and can only contain letters, digits, ".", "_", and "-"';
  }

  if (f.mode === 'template') {
    for (const key of f.template?.env_required ?? []) {
      if (!f.env[key] || !String(f.env[key]).trim()) {
        e[`env-${key}`] = `${key} is required`;
      }
    }
    for (const p of f.template?.ports ?? []) {
      const hp = f.ports[String(p.container_port)];
      if (hp !== undefined && (hp < 1 || hp > 65535)) {
        e[`port-${p.container_port}`] = 'Host port must be between 1 and 65535';
      }
    }
  } else {
    if (!f.customImage.trim()) {
      e.customImage = 'Docker image is required';
    }
    f.customEnv.forEach((row, idx) => {
      if (row.key.trim() && !row.value.trim()) {
        e[`cenv-${idx}`] = `Value required for ${row.key}`;
      }
    });
    f.customPorts.forEach((row, idx) => {
      if (row.host > 0 && (row.host < 1 || row.host > 65535 || row.container < 1 || row.container > 65535)) {
        e[`cport-${idx}`] = 'Ports must be between 1 and 65535';
      }
    });
  }

  return e;
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