// Type definitions matching Dockpal Go backend API responses

export interface User {
  id: string;
  username: string;
  role: 'admin' | 'operator' | 'viewer';
  created_at: string;
}

export interface Service {
  id: string;
  name: string;
  status: 'running' | 'stopped' | 'degraded' | 'error';
  type: 'container' | 'compose' | 'git' | 'template';
  instance_id?: string;
  domain?: string;
  created_at: string;
}

export interface Template {
  id: string;
  name: string;
  description: string;
  category: string;
  icon: string;
  icon_url?: string;
  tags?: string[];
  popular?: boolean;
  env_required: string[];
  ports: TemplatePort[];
  compose: string;
}

export interface TemplatePort {
  label: string;
  default: number;
  container_port: number;
}

// Docker PortSummary as returned by the API (matches Go's
// github.com/docker/docker/api/types/container.PortSummary JSON tags).
export interface PortSummary {
  IP?: string;
  PrivatePort: number;
  PublicPort?: number;
  Type: string;
}

export interface ContainerInfo {
  id: string;
  name: string;
  image: string;
  status: string;
  state: string;
  ports: PortSummary[];
  created: string;
  network_mode?: string;
}

// GET /api/instances — summary row per registered Docker host (incl. "local").
export interface InstanceListItem {
  id: string;
  name: string;
  host: string;
  port: number;
  mode: string;
  status: string;
  last_seen: number;
}

// GET /api/instances/:id/system/info — merged HostInfo + HostStats.
export interface SystemInfo {
  hostname: string;
  os: string;
  cpu_cores: number;
  docker_version: string;
  cpu_percent: number;
  used_ram: number;
  total_ram: number;
  used_disk: number;
  total_disk: number;
}

