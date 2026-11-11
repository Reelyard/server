import { useEffect, useState } from "react";
import { Link } from "react-router";
import { api, type Activity as Item } from "../api";
import { ago } from "../format";

const LEVEL: Record<Item["level"], string> = {
  info: "bg-deep",
  warn: "bg-accent",
  error: "bg-bad",
};

function link(titleId?: string) {
  if (!titleId) return null;
  const [imdb, s] = titleId.split(":");
  return s ? `/title/series/${imdb}?s=${s}&ep=${encodeURIComponent(titleId)}` : `/title/movie/${imdb}`;
}

export default function Activity() {
  const [items, setItems] = useState<Item[] | null>(null);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState<"" | "problems">("");

  useEffect(() => {
    let live = true;
    api.activity().then((a) => live && setItems(a)).catch((e) => live && setError(e.message));
    const es = new EventSource("/api/v1/stream");
    es.addEventListener("activity", (ev) => {
      try {
        const a = JSON.parse((ev as MessageEvent).data) as Item;
        setItems((list) => (list && !list.some((x) => x.id === a.id) ? [a, ...list] : list));
      } catch {
        /* ignore */
      }
    });
    return () => {
      live = false;
      es.close();
    };
  }, []);

  const shown = (items ?? []).filter((a) => !filter || a.level !== "info");

  return (
    <div className="flex flex-col gap-6">
      <header className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <p className="eyebrow">Activity</p>
          <h1 className="mt-1 text-2xl font-semibold tracking-tight">What Harbor did</h1>
        </div>
        <div className="flex gap-1.5" role="group" aria-label="Show">
          {(["", "problems"] as const).map((f) => (
            <button
              key={f || "all"}
              aria-pressed={filter === f}
              onClick={() => setFilter(f)}
              className={`rounded-full border px-3 py-1 text-sm ${filter === f ? "border-ink bg-ink text-bg" : "border-line bg-surface hover:bg-bg"}`}
            >
              {f ? "Problems" : "Everything"}
            </button>
          ))}
        </div>
      </header>
      {error && <p role="alert" className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">{error}</p>}
      {!items && !error && <p className="text-muted">Loading…</p>}
      {items && shown.length === 0 && (
        <div className="card border-dashed p-6 text-muted">{filter ? "No problems recorded." : "Nothing yet. Downloads, imports and subtitles will show up here."}</div>
      )}
      {shown.length > 0 && (
        <ol className="card divide-y divide-line">
          {shown.map((a) => {
            const to = link(a.titleId);
            return (
              <li key={a.id} className="flex items-start gap-3 px-4 py-2.5 text-sm">
                <span className={`mt-1.5 h-2 w-2 shrink-0 rounded-full ${LEVEL[a.level]}`} aria-label={a.level} />
                <span className="min-w-0 flex-1 break-words">
                  {to ? <Link to={to} className="hover:underline">{a.message}</Link> : a.message}
                </span>
                <time className="shrink-0 text-xs text-muted" dateTime={a.time} title={new Date(a.time).toLocaleString()}>{ago(a.time)}</time>
              </li>
            );
          })}
        </ol>
      )}
    </div>
  );
}
