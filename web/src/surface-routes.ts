// Pure routing rules for the two console surfaces (feature-17 AD1/AD7).
// The surface is a pure function of the path: /admin/... is the admin web
// surface, every other path is the user web surface.

import type { Realm } from './api';

// isAdminPath reports whether a web path belongs to the admin console.
export function isAdminPath(path: string): boolean {
  return path === '/admin' || path.startsWith('/admin/');
}

// MOVED_ADMIN_ROUTES maps the admin URLs that moved to the end-user
// console to their new paths. Evaluated before any shell or API call
// (AD7), so a bookmark does not trigger the admin session guard.
export const MOVED_ADMIN_ROUTES: Record<string, string> = {
  '/admin/api-keys': '/api-keys',
  '/admin/request-logs': '/request-logs',
  '/admin/playground': '/playground',
};

// realmHome returns the landing route of a realm.
export function realmHome(realm: Realm): string {
  return realm === 'admin' ? '/admin/models' : '/usage';
}

// realmLoginPath returns the login route of a realm.
export function realmLoginPath(realm: Realm): string {
  return realm === 'admin' ? '/admin/login' : '/login';
}

// isAllowedNext validates the next parameter of a guard redirect so the
// login page cannot be used as a surface-crossing redirector (S7).
export function isAllowedNext(realm: Realm, next: string): boolean {
  if (realm === 'admin') {
    return next === '/admin' || next.startsWith('/admin/');
  }
  return next !== '/admin' && !next.startsWith('/admin/');
}
