import { useEffect, useState, type FormEvent } from "react";
import { api, type Supplier, type SupplierTest } from "../api";
import AddonCatalog from "../components/AddonCatalog";
import { ago } from "../format";

const RESOURCE_LABEL: Record<string, string> = { stream: "Streams", subtitles: "Subtitles", catalog: "Catalogs", meta: "Metadata" };

function Status({ s }: { s: Supplier }) {
  if (!s.enabled) return <span className="rounded-full bg-bg px-2 py-0.5 text-xs text-muted">Off</span>;
  if (s.lastError) return <span className="rounded-full bg-bad-soft px-2 py-0.5 text-xs text-bad" title={s.lastError}>Problem</span>;
  if (s.lastOkAt) return <span className="rounded-full bg-ok-soft px-2 py-0.5 text-xs text-ok">Working</span>;
  return <span className="rounded-full bg-bg px-2 py-0.5 text-xs text-muted">Not checked</span>;
}

function TestPanel({ result }: { result: SupplierTest }) {
  return (
    <div className={`rounded-lg p-3 text-sm ${result.ok ? "bg-ok-soft" : "bg-bad-soft"}`}>
      <p className={result.ok ? "text-ok" : "text-bad"}>{result.message}</p>
      {result.checks.length > 0 && (
        <ul className="mt-2 flex flex-col gap-1.5 text-ink">
          {result.checks.map((c, i) => (
            <li key={i} className="min-w-0">
              <span className="font-medium">{c.title}</span>{" "}
              <span className="text-muted">
                · {c.resource === "stream" ? `${c.usable} of ${c.count} streams are torrents` : `${c.count} subtitles`} · {c.durationMs} ms
              </span>
              {c.error && <span className="block text-bad">{c.error}</span>}
              {c.samples && c.samples.length > 0 && (
                <span className="block truncate font-mono text-xs text-muted" title={c.samples.join("\n")}>
                  {c.samples.join(" · ")}
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function SupplierCard({ s, first, last, onChange, onMove }: { s: Supplier; first: boolean; last: boolean; onChange: () => void; onMove: (dir: -1 | 1) => void }) {
  const [busy, setBusy] = useState<"" | "test" | "refresh" | "toggle" | "delete">("");
  const [test, setTest] = useState<SupplierTest | null>(null);
  const [error, setError] = useState("");
  const [confirming, setConfirming] = useState(false);

  async function run(kind: typeof busy, fn: () => Promise<unknown>) {
    setBusy(kind);
    setError("");
    try {
      await fn();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Something went wrong.");
    } finally {
      setBusy("");
    }
  }

  return (
    <li className="card flex flex-col gap-3 p-4">
      <div className="flex flex-wrap items-start gap-3">
        {s.logo ? (
          <img src={s.logo} alt="" className="h-10 w-10 shrink-0 rounded-lg bg-bg object-contain" referrerPolicy="no-referrer" />
        ) : (
          <div className="grid h-10 w-10 shrink-0 place-items-center rounded-lg bg-deep-soft font-semibold text-deep">{s.name.slice(0, 1)}</div>
        )}
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="font-semibold">{s.name}</h2>
            <span className="font-mono text-xs text-muted">v{s.version}</span>
            <Status s={s} />
          </div>
          <p className="truncate font-mono text-xs text-muted" title={s.displayUrl}>{s.displayUrl}</p>
          <div className="mt-1.5 flex flex-wrap gap-1.5">
            {s.resources.map((r) => (
              <span key={r} className="rounded bg-deep-soft px-1.5 py-0.5 text-xs text-deep">{RESOURCE_LABEL[r] ?? r}</span>
            ))}
            <span className="text-xs text-muted">for {s.types.join(", ")}</span>
          </div>
        </div>
        <label className="flex cursor-pointer items-center gap-2 text-sm">
          <input
            id={`sup-enabled-${s.id}`}
            type="checkbox"
            className="h-4 w-4 accent-[var(--hb-deep)]"
            checked={s.enabled}
            disabled={busy !== ""}
            onChange={(e) => run("toggle", () => api.updateSupplier(s.id, { enabled: e.target.checked }).then(onChange))}
          />
          On
        </label>
      </div>

      {s.lastError && <p className="text-sm text-bad">Last problem ({ago(s.lastCheckedAt)}): {s.lastError}</p>}
      {test && <TestPanel result={test} />}
      {error && <p role="alert" className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">{error}</p>}

      <div className="flex flex-wrap items-center gap-2">
        <button className="btn-primary py-1.5" disabled={busy !== ""} onClick={() => run("test", async () => { setTest(await api.testSupplier(s.id)); onChange(); })}>
          {busy === "test" ? "Testing…" : "Test"}
        </button>
        <button className="btn-ghost py-1.5" disabled={busy !== ""} onClick={() => run("refresh", () => api.refreshSupplier(s.id).then(onChange))}>
          {busy === "refresh" ? "Refreshing…" : "Refresh manifest"}
        </button>
        <span className="flex gap-1">
          <button className="btn-ghost px-2.5 py-1.5" disabled={first || busy !== ""} onClick={() => onMove(-1)} aria-label={`Move ${s.name} up`} title="Search this one earlier">↑</button>
          <button className="btn-ghost px-2.5 py-1.5" disabled={last || busy !== ""} onClick={() => onMove(1)} aria-label={`Move ${s.name} down`} title="Search this one later">↓</button>
        </span>
        <span className="ml-auto text-xs text-muted">Checked {ago(s.lastCheckedAt)}</span>
        {confirming ? (
          <span className="flex items-center gap-2 text-sm">
            Remove {s.name}?
            <button className="btn-danger py-1.5" disabled={busy !== ""} onClick={() => run("delete", () => api.deleteSupplier(s.id).then(onChange))}>Remove</button>
            <button className="btn-ghost py-1.5" onClick={() => setConfirming(false)}>Keep</button>
          </span>
        ) : (
          <button className="btn-ghost py-1.5 text-bad" disabled={busy !== ""} onClick={() => setConfirming(true)}>Remove</button>
        )}
      </div>
    </li>
  );
}

export default function Suppliers() {
  const [list, setList] = useState<Supplier[] | null>(null);
  const [url, setUrl] = useState("");
  const [adding, setAdding] = useState(false);
  const [error, setError] = useState("");
  const [browsing, setBrowsing] = useState(false);

  const load = () => api.suppliers().then(setList).catch((e) => setError(e.message));
  useEffect(() => {
    load();
  }, []);

  async function add(e: FormEvent) {
    e.preventDefault();
    setAdding(true);
    setError("");
    try {
      await api.addSupplier(url);
      setUrl("");
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Couldn't add the addon.");
    } finally {
      setAdding(false);
    }
  }

  // Swap priorities with the neighbour; list is ordered highest first.
  async function move(i: number, dir: -1 | 1) {
    if (!list) return;
    const a = list[i], b = list[i + dir];
    if (!b) return;
    try {
      const pa = a.priority === b.priority ? b.priority - dir : b.priority;
      await api.updateSupplier(a.id, { priority: pa });
      await api.updateSupplier(b.id, { priority: a.priority });
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Couldn't reorder.");
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <header>
        <p className="eyebrow">Suppliers</p>
        <h1 className="mt-1 text-2xl font-semibold tracking-tight">Stremio addons</h1>
        <p className="mt-1 max-w-prose text-muted">
          Harbor asks these addons for torrents and subtitles. Paste an addon's manifest link: the one you would install in Stremio, ending in <code className="font-mono text-sm">/manifest.json</code>.
        </p>
      </header>

      <form onSubmit={add} className="card flex flex-col gap-3 p-4">
        <label className="label" htmlFor="addon-url">Addon link</label>
        <div className="flex flex-wrap gap-2">
          <input
            id="addon-url"
            className="input min-w-0 flex-1 font-mono"
            placeholder="https://torrentio.strem.fun/manifest.json"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            autoComplete="off"
            spellCheck={false}
          />
          <button className="btn-primary" disabled={adding || url.trim().length < 4}>{adding ? "Checking addon…" : "Add supplier"}</button>
        </div>
        <p className="text-xs text-muted">Links with your addon settings (and any keys) in them are stored as-is and shown shortened.</p>
        {error && <p role="alert" className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">{error}</p>}
      </form>

      {list === null && !error && <p className="text-muted">Loading suppliers…</p>}
      {list && list.length === 0 && (
        <div className="card border-dashed p-6 text-muted">
          No suppliers yet. Paste a torrent addon such as Torrentio for streams, or browse the catalog below for subtitle addons like OpenSubtitles.
        </div>
      )}
      {list && list.length > 0 && (
        <ul className="flex flex-col gap-3">
          {list.map((s, i) => (
            <SupplierCard key={s.id} s={s} first={i === 0} last={i === list.length - 1} onChange={load} onMove={(d) => move(i, d)} />
          ))}
        </ul>
      )}

      {browsing ? (
        <AddonCatalog onAdded={load} />
      ) : (
        <div>
          <button className="btn-ghost" onClick={() => setBrowsing(true)}>Browse Stremio's addon catalog</button>
        </div>
      )}
    </div>
  );
}
