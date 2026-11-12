import { useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { api, type BrowseCatalog, type BrowseItem } from "../api";
import { Poster } from "./Search";

const TYPES = [
  { v: "", label: "All" },
  { v: "movie", label: "Movies" },
  { v: "series", label: "Series" },
];

const keyOf = (c: BrowseCatalog) => `${c.supplierId}:${c.type}:${c.id}`;

// Browse shows the catalogs of suppliers that offer them, such as Nuvio's
// catalog addon (Trending, Popular, Netflix…). Titles open their page, where
// they can be tracked or downloaded.
export default function Browse() {
  const [params, setParams] = useSearchParams();
  const [catalogs, setCatalogs] = useState<BrowseCatalog[] | null>(null);
  const [items, setItems] = useState<BrowseItem[]>([]);
  const [more, setMore] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const type = params.get("type") ?? "";
  const shown = useMemo(() => (catalogs ?? []).filter((c) => !type || c.type === type), [catalogs, type]);
  const current = shown.find((c) => keyOf(c) === params.get("c")) ?? shown[0];
  const genre = current && current.genres.includes(params.get("genre") ?? "") ? params.get("genre")! : current?.genreRequired ? current.genres[0] : "";

  useEffect(() => {
    api.browseCatalogs().then(setCatalogs).catch((e) => setError(e instanceof Error ? e.message : "Couldn't load the catalogs."));
  }, []);

  function load(skip: number) {
    if (!current) return;
    let live = true;
    setLoading(true);
    setError("");
    api
      .browse(current, genre, skip)
      .then((page) => {
        if (!live) return;
        setItems((old) => {
          const base = skip === 0 ? [] : old;
          const seen = new Set(base.map((i) => i.id));
          return [...base, ...page.filter((i) => !seen.has(i.id))];
        });
        setMore(page.length > 0);
      })
      .catch((e) => live && setError(e instanceof Error ? e.message : "Couldn't load this catalog."))
      .finally(() => live && setLoading(false));
    return () => {
      live = false;
    };
  }

  const currentKey = current ? keyOf(current) : "";
  useEffect(() => load(0), [currentKey, genre]); // eslint-disable-line react-hooks/exhaustive-deps

  function set(k: string, v: string) {
    const next = new URLSearchParams(params);
    if (v) next.set(k, v);
    else next.delete(k);
    if (k !== "genre") next.delete("genre");
    if (k === "type") next.delete("c");
    setParams(next, { replace: true });
  }

  const suppliers = [...new Set(shown.map((c) => c.supplier))];

  return (
    <div className="flex flex-col gap-6">
      <header>
        <p className="eyebrow">Browse</p>
        <h1 className="mt-1 text-2xl font-semibold tracking-tight">What's on</h1>
      </header>

      {catalogs && catalogs.length === 0 && (
        <div className="card flex flex-col gap-2 p-5 text-sm">
          <p>None of your suppliers offer catalogs yet.</p>
          <p className="text-muted">
            Add a catalog addon in <Link to="/suppliers" className="text-deep underline">Suppliers</Link>, for example Nuvio's: paste its link that ends in{" "}
            <code className="font-mono">/manifest.json</code>.
          </p>
        </div>
      )}

      {catalogs && catalogs.length > 0 && (
        <div className="flex flex-col gap-3">
          <div className="flex gap-1.5" role="group" aria-label="Type">
            {TYPES.map((t) => (
              <button
                key={t.v}
                type="button"
                aria-pressed={type === t.v}
                onClick={() => set("type", t.v)}
                className={`rounded-full border px-3 py-1 text-sm ${type === t.v ? "border-ink bg-ink text-bg" : "border-line bg-surface text-ink hover:bg-bg"}`}
              >
                {t.label}
              </button>
            ))}
          </div>
          <div className="flex flex-wrap gap-3">
            <div className="min-w-0 flex-1 sm:max-w-sm">
              <label className="label" htmlFor="browse-catalog">Catalog</label>
              <select id="browse-catalog" className="input" value={currentKey} onChange={(e) => set("c", e.target.value)}>
                {suppliers.map((sup) => (
                  <optgroup key={sup} label={sup}>
                    {shown
                      .filter((c) => c.supplier === sup)
                      .map((c) => (
                        <option key={keyOf(c)} value={keyOf(c)}>
                          {c.name}
                          {!type ? (c.type === "movie" ? " (movies)" : " (series)") : ""}
                        </option>
                      ))}
                  </optgroup>
                ))}
              </select>
            </div>
            {current && current.genres.length > 0 && (
              <div className="min-w-0 sm:w-56">
                <label className="label" htmlFor="browse-genre">Filter</label>
                <select id="browse-genre" className="input" value={genre} onChange={(e) => set("genre", e.target.value)}>
                  {!current.genreRequired && <option value="">All</option>}
                  {current.genres.map((g) => (
                    <option key={g} value={g}>{g}</option>
                  ))}
                </select>
              </div>
            )}
          </div>
        </div>
      )}

      {error && <p role="alert" className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">{error}</p>}

      {items.length > 0 && (
        <ul className="grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5">
          {items.map((r) => {
            const card = (
              <>
                <Poster src={r.poster} name={r.name} className="transition-opacity group-hover:opacity-85" />
                <span className="text-sm font-medium leading-snug">{r.name}</span>
                <span className="text-xs text-muted">
                  {r.type === "movie" ? "Movie" : "Series"}
                  {r.releaseInfo ? ` · ${r.releaseInfo}` : ""}
                  {r.imdbRating ? ` · ★ ${r.imdbRating}` : ""}
                </span>
              </>
            );
            return (
              <li key={r.id}>
                {r.id.startsWith("tt") ? (
                  <Link to={`/title/${r.type}/${r.id}`} className="group flex flex-col gap-1.5 rounded-lg focus-visible:outline-2 focus-visible:outline-accent">
                    {card}
                  </Link>
                ) : (
                  <div className="flex flex-col gap-1.5 opacity-60" title="No IMDb id, so Reelyard can't open it">{card}</div>
                )}
              </li>
            );
          })}
        </ul>
      )}
      {loading && <p className="text-muted">Loading…</p>}
      {!loading && current && items.length === 0 && !error && <p className="text-muted">This catalog is empty.</p>}
      {!loading && more && items.length > 0 && (
        <div>
          <button className="btn-ghost" onClick={() => load(items.length)}>Load more</button>
        </div>
      )}
    </div>
  );
}
