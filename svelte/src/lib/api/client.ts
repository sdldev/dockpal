// Unified API client for Dockpal Go backend.
// Backend endpoints live under /api; JWT stored in sessionStorage so a token
// never survives past the browser tab (mitigates persistent XSS token theft).

const TOKEN_KEY = 'dockpal_token';

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

export function getToken(): string | null {
  return sessionStorage.getItem(TOKEN_KEY);
}

export function setToken(token: string): void {
  sessionStorage.setItem(TOKEN_KEY, token);
}

export function clearToken(): void {
  sessionStorage.removeItem(TOKEN_KEY);
}

interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE' | 'PATCH';
  body?: unknown;
  auth?: boolean;
}

async function request<T>(endpoint: string, options: RequestOptions = {}): Promise<T> {
  const { method = 'GET', body, auth = true } = options;

  const headers: Record<string, string> = {};
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json';
  }
  const token = getToken();
  if (auth && token) {
    headers['Authorization'] = `Bearer ${token}`;
  }

  const path = endpoint.startsWith('/') ? endpoint : `/${endpoint}`;
  let response: Response;
  try {
    response = await fetch(`/api${path}`, {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined
    });
  } catch {
    // fetch rejects (rather than returning an error response) when the request
    // never reaches the backend: server down, network drop, or browser offline.
    // Its TypeError message ("Failed to fetch") means nothing to a user.
    throw new ApiError(
      0,
      'Cannot reach the Dockpal server — check your connection and that the server is running.'
    );
  }

  if (!response.ok) {
    // Token expired or revoked: clear session so the app falls back to login.
    // App.svelte listens for this event to reset currentUser.
    if (response.status === 401 && auth) {
      clearToken();
      window.dispatchEvent(new Event('dockpal:unauthorized'));
    }
    // Start from a readable hint, then prefer the backend's specific message
    // when the body is JSON (it almost always is).
    let message = friendlyStatusMessage(response.status);
    try {
      const errorBody = (await response.json()) as { error?: string };
      if (errorBody.error) message = errorBody.error;
    } catch {
      // Non-JSON error body (proxy/gateway response); keep the readable hint.
    }
    throw new ApiError(response.status, message);
  }

  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

export const api = {
  get: <T>(endpoint: string) => request<T>(endpoint, { method: 'GET' }),
  post: <T>(endpoint: string, body?: unknown) => request<T>(endpoint, { method: 'POST', body }),
  put: <T>(endpoint: string, body?: unknown) => request<T>(endpoint, { method: 'PUT', body }),
  patch: <T>(endpoint: string, body?: unknown) => request<T>(endpoint, { method: 'PATCH', body }),
  delete: <T>(endpoint: string) => request<T>(endpoint, { method: 'DELETE' })
};

// friendlyStatusMessage maps a bare HTTP status to a user-readable hint. It is
// only a fallback: when the backend responds with JSON it always carries a
// specific `error` field ("insufficient permissions", "instance offline", …),
// which takes priority over these generic strings.
function friendlyStatusMessage(status: number): string {
  switch (status) {
    case 400:
      return 'The request was rejected by the server (invalid request).';
    case 401:
      return 'Authentication required — please log in again.';
    case 403:
      return 'You do not have permission to perform this action.';
    case 404:
      return 'The requested resource was not found.';
    case 409:
      return 'This action conflicts with the current state of the resource.';
    case 429:
      return 'Too many requests — please wait a moment and try again.';
    case 502:
    case 503:
    case 504:
      return 'The Dockpal server is temporarily unavailable — try again shortly.';
    default:
      return `HTTP ${status}`;
  }
}
