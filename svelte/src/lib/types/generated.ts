// Phase 4: auto-generated-like type definitions matching Go backend API responses

import type { PortSummary } from './api';

export interface ImageInfo {
	id: string;
	repo: string;
	tag: string;
	size: string;
	created: string;
	repo_digest?: string;
	has_update?: boolean;
	remote_digest?: string;
}

// Matches db.Service JSON (GET /api/services)
export interface ServiceRecord {
	id: string;
	instance_id?: string;
	name: string;
	type: string;
	domain?: string;
	compose?: string;
	repo?: string;
	created_at: number;
}

export interface ContainerStats {
	cpu_percent: number;
	memory_usage: number;
	memory_limit: number;
	memory_percent: number;
	network_rx: number;
	network_tx: number;
}

// Admin view of a user (GET /api/users) — no password hash / token version
export interface AdminUser {
	username: string;
	role: 'admin' | 'operator' | 'viewer';
}

// Matches db.APIKey JSON (GET /api/api-keys). KeyHash never returned.
export interface ApiKey {
	id: string;
	name: string;
	role: string;
	created_at: number;
}

// POST /api/api-keys response — plaintext key shown exactly once
export interface ApiKeyCreated {
	id: string;
	name: string;
	key: string;
}

// Matches db.AuditLog JSON (GET /api/audit-logs)
export interface AuditLog {
	id: string;
	timestamp: number;
	username: string;
	user_role: string;
	action: string;
	resource: string;
	status: string;
	ip_address: string;
}

// GET /api/audit-logs paginated envelope
export interface AuditLogResponse {
	logs: AuditLog[];
	total: number;
	limit: number;
	offset: number;
}

// registry.CredentialSummary JSON — token never returned, only masked
export interface RegistryCredential {
	id: string;
	registry: string;
	username: string;
	masked_token: string;
	status: string;
	created_at: number;
	updated_at: number;
	last_validated_at: number;
}

// POST /api/registries/:id/test response
export interface RegistryTestResult {
	status: string;
	message: string;
	validated_at?: number;
}

// POST /api/backup response
export interface BackupResult {
	path: string;
	checksum: string;
	checksum_verified: boolean;
	size: number;
	timestamp: string;
}

// webhookResponse JSON — secret never returned, only has_secret
export interface Webhook {
	id: string;
	instance_id: string;
	name: string;
	repo: string;
	branch: string;
	compose_file: string;
	has_secret: boolean;
	created_at: number;
}

// Matches db.Domain JSON (GET/POST /api/domains)
export interface Domain {
	id: string;
	instance_id?: string;
	domain: string;
	service: string;
	port: number;
}

// docker.AppSummary JSON (GET /api/apps)
export interface AppSummary {
	name: string;
	instance_id?: string;
	services: AppServiceSummary[];
	auto_update: boolean;
	has_update: boolean;
	last_update?: AppUpdateRecord;
}

export interface AppServiceSummary {
	name: string;
	image: string;
	state?: string;
	has_update?: boolean;
	remote_digest?: string;
}

// db.AppUpdateRecord JSON (GET /api/apps/:name/updates)
export interface AppUpdateRecord {
	attempt_id: string;
	instance_id: string;
	app: string;
	services: Record<string, ServiceUpdateInfo>;
	stage: string;
	error_code?: string;
	message?: string;
	triggered_by: string;
	started_at: number;
	updated_at: number;
	completed_at?: number;
	events?: AppUpdateEvent[];
}

export interface ServiceUpdateInfo {
	image?: string;
	from_digest?: string;
	to_digest?: string;
	status?: string;
}

export interface AppUpdateEvent {
	at: number;
	stage: string;
	message?: string;
}

// ContainerDetail from GET /containers/:id — includes all fields backend returns
export interface ContainerDetail {
	id: string;
	name: string;
	image: string;
	state: string;
	status: string;
	created: string;
	ports: PortSummary[];
	command?: string;
}

// Legacy ContainerInfo for compatibility (same shape as ContainerDetail)
export interface ContainerInfo extends ContainerDetail {}

// GET /api/system/info response — host hardware metrics + Docker version
export interface SystemInfo {
	hostname: string;
	os: string;
	cpu_cores: number;
	cpu_percent: number;
	total_ram: number;
	used_ram: number;
	total_disk: number;
	used_disk: number;
	docker_version: string;
}

