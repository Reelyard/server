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
};
