// Thin client for Harbor's /api/v1.

export type User = { id: number; username: string; createdAt: string };
export type APIKey = { id: number; name: string; prefix: string; createdAt: string; lastUsedAt?: string };
export type SystemInfo = {
  version: string;
  commit: string;
  buildDate: string;
  goVersion: string;
  os: string;
  arch: string;
  dataDir: string;
  downloadDir: string;
  libraryDir: string;
  configFile: string;
  schema: number;
  uptimeSec: number;
};

export type Supplier = {
  id: number;
  name: string;
  manifestUrl: string;
  displayUrl: string;
  addonId: string;
  version: string;
  description?: string;
  logo?: string;
  resources: string[];
  types: string[];
  configurable: boolean;
  enabled: boolean;
  priority: number;
  lastCheckedAt?: string;
  lastOkAt?: string;
  lastError?: string;
};
export type CatalogAddon = {
  name: string;
  description?: string;
  logo?: string;
  version: string;
  manifestUrl: string;
  configureUrl?: string;
  resources: string[];
  types: string[];
  official: boolean;
  configurable: boolean;
  configurationRequired: boolean;
  installed: boolean;
};
export type SupplierCheck = { resource: string; type: string; title: string; count: number; usable: number; samples?: string[]; error?: string; durationMs: number };
export type SupplierTest = { ok: boolean; message: string; checks: SupplierCheck[] };

export type MetaResult = { id: string; type: "movie" | "series"; name: string; year?: string; poster?: string };
export type Video = { id: string; title?: string; season: number; episode: number; released: string; overview?: string; thumbnail?: string };
export type Meta = {
  id: string;
  type: "movie" | "series";
  name: string;
  year?: string;
  releaseInfo?: string;
  description?: string;
  poster?: string;
  background?: string;
  runtime?: string;
  genres?: string[];
  imdbRating?: string;
  status?: string;
  videos?: Video[];
};

export type ReleaseInfo = {
  title: string;
  year?: number;
  seasons?: number[];
  episodes?: number[];
  seasonPack?: boolean;
  complete?: boolean;
  resolution?: string;
  source?: string;
  codec?: string;
  bitDepth?: number;
  hdr?: string[];
  audio?: string[];
  channels?: string;
  languages?: string[];
  multi?: boolean;
  group?: string;
  upscaled?: boolean;
};
export type Candidate = {
  key: string;
  supplierId: number;
  supplierName: string;
  alsoFrom?: string[];
  infoHash?: string;
  fileIdx?: number;
  stream: { torrentName: string; filePath?: string; info: ReleaseInfo; seeders: number; size?: number; site?: string; label?: string };
  usable: boolean;
  problems?: string[];
};
export type StreamSearch = {
  type: string;
  id: string;
  season?: number;
  episode?: number;
  candidates: Candidate[];
  suppliers: { id: number; name: string; count: number; error?: string; durationMs: number }[];
};

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch("/api/v1" + path, {
    method,
    credentials: "same-origin",
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (res.status === 204) return undefined as T;
  let data: unknown = null;
  try {
    data = await res.json();
  } catch {
    /* empty or non-JSON body */
  }
  if (!res.ok) {
    const msg = (data as { error?: string } | null)?.error ?? `Request failed (${res.status})`;
    if (res.status === 401) window.dispatchEvent(new Event("harbor:signed-out"));
    throw new ApiError(res.status, msg);
  }
  return data as T;
}

export const api = {
  setupNeeded: () => request<{ needed: boolean }>("GET", "/setup").then((r) => r.needed),
  setup: (username: string, password: string) =>
    request<{ user: User }>("POST", "/setup", { username, password }).then((r) => r.user),
  login: (username: string, password: string) =>
    request<{ user: User }>("POST", "/auth/login", { username, password }).then((r) => r.user),
  logout: () => request<void>("POST", "/auth/logout"),
  me: () => request<{ user: User }>("GET", "/auth/me").then((r) => r.user),
  changePassword: (current: string, next: string) =>
    request<void>("POST", "/auth/password", { current, new: next }),
  listKeys: () => request<{ keys: APIKey[] }>("GET", "/auth/keys").then((r) => r.keys),
  createKey: (name: string) => request<{ key: APIKey; secret: string }>("POST", "/auth/keys", { name }),
  deleteKey: (id: number) => request<void>("DELETE", `/auth/keys/${id}`),
  system: () => request<SystemInfo>("GET", "/system"),

  suppliers: () => request<{ suppliers: Supplier[] }>("GET", "/suppliers").then((r) => r.suppliers),
  supplierCatalog: () => request<{ addons: CatalogAddon[] }>("GET", "/suppliers/catalog").then((r) => r.addons),
  addSupplier: (url: string) => request<{ supplier: Supplier }>("POST", "/suppliers", { url }).then((r) => r.supplier),
  updateSupplier: (id: number, patch: { name?: string; enabled?: boolean; priority?: number }) =>
    request<{ supplier: Supplier }>("PATCH", `/suppliers/${id}`, patch).then((r) => r.supplier),
  deleteSupplier: (id: number) => request<void>("DELETE", `/suppliers/${id}`),
  refreshSupplier: (id: number) => request<{ supplier: Supplier }>("POST", `/suppliers/${id}/refresh`).then((r) => r.supplier),
  testSupplier: (id: number) => request<SupplierTest>("POST", `/suppliers/${id}/test`),

  searchTitles: (q: string, type = "") =>
    request<{ results: MetaResult[] }>("GET", `/metadata/search?q=${encodeURIComponent(q)}${type ? `&type=${type}` : ""}`).then((r) => r.results),
  meta: (type: string, id: string) => request<{ meta: Meta }>("GET", `/metadata/${type}/${encodeURIComponent(id)}`).then((r) => r.meta),
  streams: (type: string, id: string) => request<StreamSearch>("GET", `/streams/${type}/${id}`),
};
