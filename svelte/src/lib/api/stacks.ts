// Typed client for the Dockge-style /api/stacks endpoints.
// Instance-aware: "local" targets /api/stacks, any other instance id targets
// /api/instances/<id>/stacks (mirrored routes on the server).

import { api } from './client';

export interface StackService {
  name: string;
  containerName: string;
  image: string;
  state: string;
  health: string;
}

export interface Stack {
  name: string;
  status: string; // unknown | draft | running | exited | partial
  statusText: string;
  managed: boolean;
  composeYAML?: string;
  composeENV?: string;
  services?: StackService[];
}

export interface StackListResponse {
  stacks: Stack[] | null;
}

export interface InstanceListItem {
  id: string;
  name: string;
  host: string;
  port: number;
  mode: string;
  status: string;
  last_seen: number;
}

/** Base path for stack endpoints on the given instance. */
export function stacksBasePath(instanceId: string): string {
  return instanceId === 'local' || !instanceId
    ? '/stacks'
    : `/instances/${encodeURIComponent(instanceId)}/stacks`;
}

export function listInstances(): Promise<{ instances: InstanceListItem[] } | InstanceListItem[]> {
  return api.get<{ instances: InstanceListItem[] } | InstanceListItem[]>('/instances');
}

export function listStacks(instanceId = 'local'): Promise<StackListResponse> {
  return api.get<StackListResponse>(stacksBasePath(instanceId));
}

export function listDockerNetworks(instanceId = 'local'): Promise<{ networks: string[] | null }> {
  return api.get<{ networks: string[] | null }>(`${stacksBasePath(instanceId)}/meta/networks`);
}

export function getGlobalEnv(instanceId = 'local'): Promise<{ content: string }> {
  return api.get<{ content: string }>(`${stacksBasePath(instanceId)}/meta/globalenv`);
}

export function setGlobalEnv(content: string, instanceId = 'local'): Promise<{ message: string }> {
  return api.put<{ message: string }>(`${stacksBasePath(instanceId)}/meta/globalenv`, { content });
}

export function getStack(name: string, instanceId = 'local'): Promise<Stack> {
  return api.get<Stack>(`${stacksBasePath(instanceId)}/${encodeURIComponent(name)}`);
}

export function createStack(name: string, compose: string, env: string, instanceId = 'local'): Promise<Stack> {
  return api.post<Stack>(stacksBasePath(instanceId), { name, compose, env });
}

export function updateStackFiles(name: string, compose: string, env: string, instanceId = 'local'): Promise<Stack> {
  return api.put<Stack>(`${stacksBasePath(instanceId)}/${encodeURIComponent(name)}`, { compose, env });
}

export function deleteStack(name: string, instanceId = 'local'): Promise<{ message: string }> {
  return api.delete<{ message: string }>(`${stacksBasePath(instanceId)}/${encodeURIComponent(name)}`);
}

export type StackAction = 'up' | 'start' | 'stop' | 'restart' | 'down' | 'update';

export function stackAction(name: string, action: StackAction, instanceId = 'local'): Promise<Stack> {
  return api.post<Stack>(`${stacksBasePath(instanceId)}/${encodeURIComponent(name)}/${action}`);
}

export function deployStack(
  name: string,
  compose: string,
  env: string,
  isAdd: boolean,
  instanceId = 'local'
): Promise<{ deploy_id: string }> {
  return api.post<{ deploy_id: string }>(
    `${stacksBasePath(instanceId)}/${encodeURIComponent(name)}/deploy`,
    { compose, env, is_add: isAdd }
  );
}

export type ServiceAction = 'up' | 'stop' | 'restart';

export function stackServiceAction(
  name: string,
  service: string,
  action: ServiceAction,
  instanceId = 'local'
): Promise<Stack> {
  return api.post<Stack>(
    `${stacksBasePath(instanceId)}/${encodeURIComponent(name)}/services/${encodeURIComponent(service)}/${action}`
  );
}

/**
 * Subscribe to a deploy session's event stream over the existing deploy WS
 * endpoint. The instance-shaped URL works for local too (the WS handler only
 * needs the session id).
 */
export function watchDeploy(
  deployId: string,
  onEvent: (msg: { step?: string; message?: string; status?: string; time?: string }) => void,
  onDone?: () => void,
  instanceId = 'local'
): () => void {
  const token = localStorage.getItem('dockpal_token') ?? '';
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const ws = new WebSocket(
    `${proto}//${window.location.host}/api/instances/${encodeURIComponent(instanceId)}/deploy/stream/${encodeURIComponent(deployId)}?token=${encodeURIComponent(token)}`
  );
  ws.onmessage = (event) => {
    try {
      onEvent(JSON.parse(event.data));
    } catch {
      onEvent({ message: String(event.data) });
    }
  };
  ws.onclose = () => onDone?.();
  ws.onerror = () => onDone?.();
  return () => ws.close();
}