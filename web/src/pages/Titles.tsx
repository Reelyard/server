import { useEffect, useState } from "react";
import { Link } from "react-router";
import { api, type Tracked } from "../api";
import { Poster } from "./Search";

const FILTERS = [
  { v: "", label: "All" },
  { v: "wanted", label: "Wanted" },
  { v: "movie", label: "Movies" },
  { v: "series", label: "Series" },
];

function status(t: Tracked) {
  if (t.type === "movie") {
    if (t.imported) return { text: "In library", cls: "bg-ok-soft text-ok" };
    if (t.downloading) return { text: "Downloading", cls: "bg-deep-soft text-deep" };
    if (t.unaired) return { text: "Not released", cls: "bg-bg text-muted" };
    return { text: "Wanted", cls: "bg-accent-soft text-accent" };
  }
  const parts = [`${t.imported} in library`];
  if (t.downloading) parts.push(`${t.downloading} downloading`);
  if (t.wanted) parts.push(`${t.wanted} wanted`);
  return { text: parts.join(" · "), cls: t.wanted ? "bg-accent-soft text-accent" : "bg-ok-soft text-ok" };
}

export default function Titles() {
  const [list, setList] = useState<Tracked[] | null>(null);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState("");

  useEffect(() => {
    api.titles().then(setList).catch((e) => setError(e.message));
  }, []);

  const shown = (list ?? []).filter((t) => !filter || (filter === "wanted" ? t.wanted > 0 : t.type === filter));

  return (
    <div className="flex flex-col gap-6">
      <header className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <p className="eyebrow">Titles</p>
          <h1 className="mt-1 text-2xl font-semibold tracking-tight">Tracked titles</h1>
          <p className="mt-1 text-muted">Reelyard looks for these by itself and downloads what fits their quality profile.</p>
        </div>
        <div className="flex gap-1.5" role="group" aria-label="Show">
          {FILTERS.map((f) => (
            <button
              key={f.v}
              aria-pressed={filter === f.v}
              onClick={() => setFilter(f.v)}
              className={`rounded-full border px-3 py-1 text-sm ${filter === f.v ? "border-ink bg-ink text-bg" : "border-line bg-surface hover:bg-bg"}`}
            >
              {f.label}
            </button>
          ))}
        </div>
      </header>
      {error && <p role="alert" className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">{error}</p>}
      {!list && !error && <p className="text-muted">Loading…</p>}
      {list && list.length === 0 && (
        <div className="card border-dashed p-6 text-muted">
          Nothing tracked yet. Find a movie or series in <Link to="/search" className="text-deep underline">Search</Link> and press <b>Track</b>.
        </div>
      )}
      {shown.length > 0 && (
        <ul className="grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5">
          {shown.map((t) => {
            const s = status(t);
            return (
              <li key={t.id}>
                <Link to={`/title/${t.type}/${t.imdbId}`} className="group flex flex-col gap-1.5 rounded-lg focus-visible:outline-2 focus-visible:outline-accent">
                  <Poster src={t.poster} name={t.name} className="transition-opacity group-hover:opacity-85" />
                  <span className="text-sm leading-snug font-medium">{t.name}</span>
                  <span className="text-xs text-muted">{t.type === "movie" ? "Movie" : "Series"}{t.year ? ` · ${t.year}` : ""}</span>
                  <span className={`self-start rounded px-1.5 py-0.5 text-xs ${s.cls}`}>{s.text}</span>
                </Link>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
