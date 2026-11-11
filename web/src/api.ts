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
  sources?: string[];
  stream: { torrentName: string; filePath?: string; info: ReleaseInfo; seeders: number; size?: number; site?: string; label?: string };
  usable: boolean;
  problems?: string[];
  eval?: Evaluation;
};
export type StreamSearch = {
  type: string;
  id: string;
  season?: number;
  episode?: number;
  candidates: Candidate[];
  suppliers: { id: number; name: string; count: number; error?: string; durationMs: number }[];
  profile?: string;
  summary?: string;
};

export type Download = {
  id: number;
  infoHash: string;
  fileIdx: number;
  name: string;
  filePath?: string;
  titleType?: string;
  titleId?: string;
  titleName?: string;
  state: "queued" | "metadata" | "downloading" | "paused" | "seeding" | "failed";
  error?: string;
  size: number;
  doneBytes: number;
  uploaded: number;
  addedAt: string;
  completedAt?: string;
  importState?: "" | "importing" | "imported" | "failed" | "skipped";
  importError?: string;
  progress: number;
  downRate: number;
  upRate: number;
  peers: number;
  seeds: number;
  eta: number;
  ratio: number;
};
export type DownloadSettings = { maxActive: number; downloadLimitKBps: number; uploadLimitKBps: number };
export type NewDownload = {
  infoHash: string;
  fileIdx?: number;
  sources?: string[];
  name: string;
  titleType?: string;
  titleId?: string;
  titleName?: string;
};

export type SubtitleFile = { id: number; mediaFileId: number; lang: string; path: string; source: string; createdAt: string };
export type MediaFile = {
  id: number;
  downloadId?: number;
  titleType: string;
  titleId: string;
  titleName: string;
  path: string;
  size: number;
  method: "hardlink" | "copy";
  resolution?: string;
  videoCodec?: string;
  durationSec?: number;
  audioLangs: string[];
  embeddedSubs: string[];
  importedAt: string;
  subtitles: SubtitleFile[];
};
export type SubtitleResult = { lang: string; status: "embedded" | "present" | "saved" | "none" | "failed"; path?: string; source?: string; error?: string };
export type Activity = { id: number; time: string; level: "info" | "warn" | "error"; kind: string; titleId?: string; message: string };
export type LibrarySettings = { subtitleLangs: string[] };
export type Language = { code: string; name: string };

export type Profile = {
  id: number;
  name: string;
  resolutions: string[];
  movieMinMB: number;
  movieMaxMB: number;
  episodeMinMB: number;
  episodeMaxMB: number;
  audioLangs: string[];
  audioRequired: boolean;
  subtitleLangs: string[];
  blockedTerms: string[];
  preferredTerms: string[];
  minSeeders: number;
  seedRatio: number;
  seedHours: number;
};
export type Tracked = {
  id: number;
  imdbId: string;
  type: "movie" | "series";
  name: string;
  year?: number;
  poster?: string;
  profileId: number;
  monitor: "all" | "future" | "none";
  seasons: number[];
  addedAt: string;
  refreshedAt?: string;
  wanted: number;
  downloading: number;
  imported: number;
  unaired: number;
};
export type Item = {
  id: number;
  titleId: number;
  itemId: string;
  season: number;
  episode: number;
  name?: string;
  airDate?: string;
  monitored: boolean;
  state: "unaired" | "missing" | "downloading" | "imported";
  downloadId?: number;
  attempts: number;
  nextSearchAt?: string;
  lastSearchAt?: string;
  note?: string;
};
export type Evaluation = { accepted: boolean; score: number; rejections?: string[]; notes?: string[] };

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
  downloads: () => request<{ downloads: Download[]; port: number }>("GET", "/downloads"),
  addDownload: (d: NewDownload) => request<{ download: Download }>("POST", "/downloads", d).then((r) => r.download),
  pauseDownload: (id: number) => request<{ download: Download }>("POST", `/downloads/${id}/pause`),
  resumeDownload: (id: number) => request<{ download: Download }>("POST", `/downloads/${id}/resume`),
  removeDownload: (id: number, deleteData: boolean) => request<void>("DELETE", `/downloads/${id}${deleteData ? "?deleteData=true" : ""}`),
  downloadSettings: () => request<{ settings: DownloadSettings; port: number }>("GET", "/settings/downloads"),
  saveDownloadSettings: (s: DownloadSettings) => request<{ settings: DownloadSettings; port: number }>("PUT", "/settings/downloads", s),

  importDownload: (id: number) => request<{ queued: boolean }>("POST", `/downloads/${id}/import`),
  library: (title = "") => request<{ files: MediaFile[]; libraryDir: string }>("GET", `/library${title ? `?title=${encodeURIComponent(title)}` : ""}`),
  fetchSubtitles: (id: number) => request<{ results: SubtitleResult[]; file: MediaFile }>("POST", `/library/${id}/subtitles`),
  activity: (limit = 200) => request<{ activity: Activity[] }>("GET", `/activity?limit=${limit}`).then((r) => r.activity),
  librarySettings: () => request<{ settings: LibrarySettings; libraryDir: string }>("GET", "/settings/library"),
  saveLibrarySettings: (s: LibrarySettings) => request<{ settings: LibrarySettings; libraryDir: string }>("PUT", "/settings/library", s),
  languages: () => request<{ languages: Language[] }>("GET", "/languages").then((r) => r.languages),

  streams: (type: string, id: string, profileId?: number) =>
    request<StreamSearch>("GET", `/streams/${type}/${id}${profileId ? `?profile=${profileId}` : ""}`),

  profiles: () => request<{ profiles: Profile[]; resolutions: string[] }>("GET", "/profiles"),
  saveProfile: (p: Partial<Profile>) =>
    (p.id ? request<{ profile: Profile }>("PUT", `/profiles/${p.id}`, { ...p, id: undefined }) : request<{ profile: Profile }>("POST", "/profiles", p)).then((r) => r.profile),
  deleteProfile: (id: number) => request<void>("DELETE", `/profiles/${id}`),
  titles: () => request<{ titles: Tracked[] }>("GET", "/titles").then((r) => r.titles),
  trackedByImdb: (imdb: string) => request<{ title: Tracked; items: Item[] }>("GET", `/titles/by-imdb/${imdb}`),
  track: (b: { imdbId: string; type: string; profileId: number; monitor: string; seasons?: number[] }) =>
    request<{ title: Tracked; items: Item[] }>("POST", "/titles", b),
  updateTracked: (id: number, b: { profileId: number; monitor: string; seasons: number[] }) =>
    request<{ title: Tracked; items: Item[] }>("PATCH", `/titles/${id}`, b),
  untrack: (id: number) => request<void>("DELETE", `/titles/${id}`),
  searchTitleNow: (id: number) => request<void>("POST", `/titles/${id}/search`),
  searchItemNow: (id: number) => request<void>("POST", `/items/${id}/search`),
  setItemMonitored: (id: number, monitored: boolean) => request<{ item: Item }>("PATCH", `/items/${id}`, { monitored }),
};
