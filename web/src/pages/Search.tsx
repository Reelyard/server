import { useEffect, useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router";
import { api, type MetaResult } from "../api";

const TYPES = [
  { v: "", label: "All" },
  { v: "movie", label: "Movies" },
  { v: "series", label: "Series" },
];

export function Poster({ src, name, className = "" }: { src?: string; name: string; className?: string }) {
  const [failed, setFailed] = useState(false);
  if (!src || failed) {
    return (
      <div className={`grid aspect-[2/3] place-items-center rounded-lg bg-deep-soft p-2 text-center text-sm font-medium text-deep ${className}`}>
        {name}
      </div>
    );
  }
  return <img src={src} alt="" loading="lazy" referrerPolicy="no-referrer" onError={() => setFailed(true)} className={`aspect-[2/3] w-full rounded-lg bg-bg object-cover ${className}`} />;
}

export default function Search() {
  const [params, setParams] = useSearchParams();
  const q = params.get("q") ?? "";
  const type = params.get("type") ?? "";
  const [text, setText] = useState(q);
  const [results, setResults] = useState<MetaResult[] | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (q.trim().length < 2) {
      setResults(null);
      return;
    }
    let live = true;
    setLoading(true);
    setError("");
    api
      .searchTitles(q, type)
      .then((r) => live && setResults(r))
      .catch((e) => live && setError(e.message))
      .finally(() => live && setLoading(false));
    return () => {
      live = false;
    };
  }, [q, type]);

  function submit(e: FormEvent) {
    e.preventDefault();
    const next = new URLSearchParams(params);
    next.set("q", text.trim());
    setParams(next);
  }

  function setType(v: string) {
    const next = new URLSearchParams(params);
    if (v) next.set("type", v);
    else next.delete("type");
    setParams(next);
  }

  return (
    <div className="flex flex-col gap-6">
      <header>
        <p className="eyebrow">Search</p>
        <h1 className="mt-1 text-2xl font-semibold tracking-tight">Find a movie or series</h1>
      </header>

      <form onSubmit={submit} className="flex flex-col gap-3">
        <div className="flex flex-wrap gap-2">
          <label htmlFor="q" className="sr-only">Title</label>
          <input id="q" className="input min-w-0 flex-1" placeholder="e.g. Breaking Bad" value={text} onChange={(e) => setText(e.target.value)} autoFocus />
          <button className="btn-primary" disabled={text.trim().length < 2}>Search</button>
        </div>
        <div className="flex gap-1.5" role="group" aria-label="Type">
          {TYPES.map((t) => (
            <button
              key={t.v}
              type="button"
              aria-pressed={type === t.v}
              onClick={() => setType(t.v)}
              className={`rounded-full border px-3 py-1 text-sm ${type === t.v ? "border-ink bg-ink text-bg" : "border-line bg-surface text-ink hover:bg-bg"}`}
            >
              {t.label}
            </button>
          ))}
        </div>
      </form>

      {error && <p role="alert" className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">{error}</p>}
      {loading && <p className="text-muted">Searching…</p>}
      {!loading && results && results.length === 0 && <p className="text-muted">Nothing found for “{q}”.</p>}
      {!loading && results && results.length > 0 && (
        <ul className="grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5">
          {results.map((r) => (
            <li key={r.type + r.id}>
              <Link to={`/title/${r.type}/${r.id}`} className="group flex flex-col gap-1.5 rounded-lg focus-visible:outline-2 focus-visible:outline-accent">
                <Poster src={r.poster} name={r.name} className="transition-opacity group-hover:opacity-85" />
                <span className="text-sm font-medium leading-snug">{r.name}</span>
                <span className="text-xs text-muted">{r.type === "movie" ? "Movie" : "Series"}{r.year ? ` · ${r.year}` : ""}</span>
              </Link>
            </li>
          ))}
        </ul>
      )}
      {!results && !loading && !error && <p className="text-muted">Search uses Cinemeta, the same catalogue Stremio uses.</p>}
    </div>
  );
}
