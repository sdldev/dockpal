// Type definitions matching Dockpal Go backend API responses

export interface User {
  id: string;
  username: string;
  role: 'admin' | 'operator' | 'viewer';
  created_at: string;
}

export interface LoginResponse {
  token: string;
  user: User;
}

export interface Service {
  id: string;
  name: string;
  status: 'running' | 'stopped' | 'degraded' | 'error';
  type: 'container' | 'compose' | 'git' | 'template';
  instance_id?: string;
  ports: ServicePort[];
  domain?: string;
  created_at: string;
}

export interface ServicePort {
  label: string;
  host_port: number;
  container_port: number;
}

export interface Template {
  id: string;
  name: string;
  description: string;
  category: string;
  icon: string;
  env_required: string[];
  ports: TemplatePort[];
  compose: string;
}

export interface TemplatePort {
  label: string;
  default: number;
  container_port: number;
}

export interface ContainerInfo {
  id: string;
  name: string;
  image: string;
  status: string;
  state: string;
  ports: string[];
  created: string;
  network_mode?: string;
}

export interface HealthStatus {
  status: 'healthy' | 'unhealthy';
  version: string;
  uptime_seconds: number;
  components: Record<string, string>;
}

export interface ApiResponse<T> {
  success: boolean;
  data?: T;
  error?: string;
}
