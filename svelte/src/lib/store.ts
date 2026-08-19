// Svelte stores for global app state

import { writable, derived } from 'svelte/store';
import type { User, Service, Template } from './types/api';

export const currentUser = writable<User | null>(null);
export const isAuthenticated = derived(currentUser, ($user) => $user !== null);

// Mirrors the legacy frontend convention: selected instance id in localStorage
export const selectedInstance = writable<string>(
  typeof localStorage !== 'undefined'
    ? localStorage.getItem('dockpal_selected_instance') || 'local'
    : 'local'
);

export const currentPage = writable<string>('dashboard');

export const services = writable<Service[]>([]);
export const templates = writable<Template[]>([]);

export const toasts = writable<
  Array<{ id: string; message: string; type: 'success' | 'error' | 'info' }>
>([]);

let toastCounter = 0;

export function addToast(message: string, type: 'success' | 'error' | 'info' = 'info'): void {
  const id = `toast-${++toastCounter}`;
  toasts.update((list) => [...list, { id, message, type }]);
  setTimeout(() => removeToast(id), 4000);
}

export function removeToast(id: string): void {
  toasts.update((list) => list.filter((t) => t.id !== id));
}
