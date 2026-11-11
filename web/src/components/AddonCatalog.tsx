import { useEffect, useMemo, useState } from "react";
import { api, type CatalogAddon } from "../api";

const FILTERS = [
  { v: "", label: "All" },
  { v: "stream", label: "Streams" },
  { v: "subtitles", label: "Subtitles" },
];

function Row({ a, onAdded }: { a: CatalogAddon; onAdded: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [added, setAdded] = useState(a.installed);

  async function add() {
    setBusy(true);
    setError("");
    try {
      await api.addSupplier(a.manifestUrl);
      setAdded(true);
      onAdded();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't add it.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <li className="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-start sm:gap-3">
      <div className="flex min-w-0 flex-1 gap-3">
        {a.logo ? (
          <img src={a.logo} alt="" loading="lazy" referrerPolicy="no-referrer" className="h-9 w-9 shrink-0 rounded-lg bg-bg object-contain" />
        ) : (
          <div className="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-deep-soft text-sm font-semibold text-deep">{a.name.slice(0, 1)}</div>
        )}
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="font-medium">{a.name}</span>
            {a.official && <span className="rounded bg-accent-soft px-1.5 py-0.5 text-xs text-accent">Official</span>}
            {a.resources.map((r) => (
              <span key={r} className="rounded bg-deep-soft px-1.5 py-0.5 text-xs text-deep">{r === "stream" ? "Streams" : "Subtitles"}</span>
            ))}
          </div>
          {a.description && <p className="line-clamp-2 text-sm text-muted" title={a.description}>{a.description}</p>}
          {error && <p role="alert" className="mt-1 text-sm text-bad">{error}</p>}
          {a.configurationRequired && !added && (
            <p className="mt-1 text-xs text-muted">Needs setting up first: open its settings page, then paste the install link it gives you into “Addon link” above.</p>
          )}
        </div>
      </div>
      <div className="flex shrink-0 items-center gap-2 sm:pt-0.5">
        {a.configureUrl && (
          <a href={a.configureUrl} target="_blank" rel="noreferrer noopener" className="btn-ghost py-1.5">
            Settings ↗
          </a>
        )}
        {added ? (
          <span className="rounded-lg bg-ok-soft px-3 py-1.5 text-sm text-ok">Added</span>
        ) : (
          !a.configurationRequired && (
            <button className="btn-primary py-1.5" disabled={busy} onClick={add}>{busy ? "Adding…" : "Add"}</button>
          )
        )}
      </div>
    </li>
  );
}

export default function AddonCatalog({ onAdded }: { onAdded: () => void }) {
  const [addons, setAddons] = useState<CatalogAddon[] | null>(null);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState("");
  const [query, setQuery] = useState("");

  useEffect(() => {
    api.supplierCatalog().then(setAddons).catch((e) => setError(e.message));
  }, []);

  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    return (addons ?? []).filter(
      (a) => (!filter || a.resources.includes(filter)) && (!q || a.name.toLowerCase().includes(q) || a.description?.toLowerCase().includes(q)),
    );
  }, [addons, filter, query]);

  return (
    <section className="flex flex-col gap-3">
      <div>
        <h2 className="text-lg font-semibold">Browse Stremio's addon catalog</h2>
        <p className="max-w-prose text-sm text-muted">
          Official and community addons that offer streams or subtitles for movies and series. Community addons are run by third parties. Torrentio and most torrent addons aren't listed here; add those by pasting their link above.
        </p>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex gap-1.5" role="group" aria-label="Show">
          {FILTERS.map((f) => (
            <button
              key={f.v}
              type="button"
              aria-pressed={filter === f.v}
              onClick={() => setFilter(f.v)}
              className={`rounded-full border px-3 py-1 text-sm ${filter === f.v ? "border-ink bg-ink text-bg" : "border-line bg-surface hover:bg-bg"}`}
            >
              {f.label}
            </button>
          ))}
        </div>
        <label htmlFor="catalog-filter" className="sr-only">Filter addons</label>
        <input id="catalog-filter" className="input min-w-0 flex-1 py-1.5 sm:max-w-xs" placeholder="Filter by name" value={query} onChange={(e) => setQuery(e.target.value)} />
      </div>
      {error && <p role="alert" className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">{error}</p>}
      {!addons && !error && <p className="text-muted">Loading the catalog…</p>}
      {addons && (
        shown.length > 0 ? (
          <ul className="card divide-y divide-line">{shown.map((a) => <Row key={a.manifestUrl} a={a} onAdded={onAdded} />)}</ul>
        ) : (
          <p className="text-muted">No addons match.</p>
        )
      )}
    </section>
  );
}
