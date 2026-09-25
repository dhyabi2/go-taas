// Surface context (feature-17): provides the realm and the realm-scoped
// API client to the pages of each console. Each surface boots by adopting
// the legacy storage keys (AD9) before the first render.

import { createContext, useContext, type ReactNode } from 'react';
import { adoptLegacyStorage, createApi, type Realm } from './api';

export interface SurfaceApi {
  get: <T>(path: string, orgId: string) => Promise<T>;
  post: <T>(path: string, orgId: string, body?: unknown) => Promise<T>;
  put: <T>(path: string, orgId: string, body?: unknown) => Promise<T>;
  del: <T>(path: string, orgId: string) => Promise<T>;
  patch: <T>(path: string, orgId: string, body?: unknown) => Promise<T>;
}

const SurfaceContext = createContext<{ realm: Realm; api: SurfaceApi }>({
  realm: 'admin',
  api: createApi('admin'),
});

export function SurfaceProvider({
  realm,
  children,
}: {
  realm: Realm;
  children: ReactNode;
}) {
  adoptLegacyStorage(realm);
  const api = createApi(realm);
  return (
    <SurfaceContext.Provider value={{ realm, api }}>{children}</SurfaceContext.Provider>
  );
}

export function useApi(): SurfaceApi {
  return useContext(SurfaceContext).api;
}

export function useRealm(): Realm {
  return useContext(SurfaceContext).realm;
}
