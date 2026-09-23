// Phase 5: Health check client for backend /health endpoint (not /api/health)

export type HealthStatus = 'healthy' | 'degraded' | 'unhealthy';
export type CheckStatus = 'pass' | 'warn' | 'fail';

export interface HealthCheckResult {
	status: CheckStatus;
	description?: string;
	duration?: string;
	details?: Record<string, unknown>;
}

export interface HealthResponse {
	status: HealthStatus;
	timestamp: string;
	uptime: string;
	uptime_seconds: number;
	uptime_source: 'host' | 'process';
	version: string;
	checks: Record<string, HealthCheckResult>;
}

/**
 * Fetch health status from /health (registered at internal/server/routes.go + internal/health/handlers.go)
 */
export async function getHealthStatus(): Promise<HealthResponse> {
	const response = await fetch('/health');
	if (!response.ok) throw new Error(`Health check failed: ${response.status}`);
	return response.json();
}

/**
 * Start background health monitoring with configurable interval
 */
export function startHealthMonitoring(intervalMs: number = 60000) {
	setInterval(async () => {
		try {
			const health = await getHealthStatus();
			if (health.status !== 'healthy') {
				console.warn('Dockpal health check degraded:', health);
			} else {
				console.log(`Health OK: v${health.version}, ${Math.floor(health.uptime_seconds / 3600)}h uptime`);
			}
		} catch (e) {
			console.error('Health monitor error:', e);
		}
	}, intervalMs);
}
