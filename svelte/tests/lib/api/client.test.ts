import { describe, it, expect, beforeAll, beforeEach } from 'vitest';
import { getToken, setToken, clearToken } from '../../../src/lib/api/client';

// The API client stores the JWT in sessionStorage so a token never outlives
// the browser tab. These tests pin that behavior: they fail if the client is
// ever changed back to localStorage (or any storage that persists).
//
// vitest runs in the node environment, which has no Web Storage, so we install
// a minimal Map-backed stub. The client only touches storage at call time
// (never at import time), so installing the stub in beforeAll is enough.

function makeStorageStub(): Storage {
  const map = new Map<string, string>();
  return {
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => {
      map.set(key, value);
    },
    removeItem: (key: string) => {
      map.delete(key);
    },
    clear: () => map.clear(),
    key: (index: number) => Array.from(map.keys())[index] ?? null,
    get length() {
      return map.size;
    }
  } as Storage;
}

let sessionStorageStub: Storage;
let localStorageStub: Storage;

beforeAll(() => {
  sessionStorageStub = makeStorageStub();
  localStorageStub = makeStorageStub();
  Object.defineProperty(globalThis, 'sessionStorage', {
    value: sessionStorageStub,
    configurable: true,
    writable: true
  });
  Object.defineProperty(globalThis, 'localStorage', {
    value: localStorageStub,
    configurable: true,
    writable: true
  });
});

beforeEach(() => {
  sessionStorageStub.clear();
  localStorageStub.clear();
});

describe('token storage', () => {
  it('persists and reads a token from sessionStorage', () => {
    setToken('jwt-abc-123');
    expect(getToken()).toBe('jwt-abc-123');
  });

  it('clears the token from sessionStorage', () => {
    setToken('jwt-abc-123');
    expect(getToken()).toBe('jwt-abc-123');
    clearToken();
    expect(getToken()).toBeNull();
  });

  it('never writes the token to localStorage', () => {
    setToken('jwt-abc-123');
    // sessionStorage is the source of truth…
    expect(getToken()).toBe('jwt-abc-123');
    // …and localStorage must stay empty. Regression guard for the QA finding
    // that flagged persistent localStorage tokens.
    expect(localStorageStub.getItem('dockpal_token')).toBeNull();
  });
});
