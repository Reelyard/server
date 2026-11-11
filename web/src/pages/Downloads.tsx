import { useState } from "react";
import { Link } from "react-router";
import { api, type Download } from "../api";
import { bytes, duration, rate } from "../format";
import { useDownloads } from "../useDownloads";

const STATE: Record<Download["state"], { label: string; cls: string }> = {
  queued: { label: "Queued", cls: "bg-bg text-muted" },
  metadata: { label: "Finding peers", cls: "bg-deep-soft text-deep" },
  downloading: { label: "Downloading", cls: "bg-deep-soft text-deep" },
  paused: { label: "Paused", cls: "bg-bg text-muted" },
  seeding: { label: "Done · sharing", cls: "bg-ok-soft text-ok" },
  failed: { label: "Failed", cls: "bg-bad-soft text-bad" },
};

export function StateChip({ state }: { state: Download["state"] }) {
  const s = STATE[state];
  return <span className={`rounded-full px-2 py-0.5 text-xs whitespace-nowrap ${s.cls}`}>{s.label}</span>;
}

export function ProgressBar({ d }: { d: Download }) {
  const pct = Math.min(100, Math.round(d.progress * 1000) / 10);
  const color = d.state === "seeding" ? "bg-ok" : d.state === "failed" ? "bg-bad" : d.state === "paused" || d.state === "queued" ? "bg-muted" : "bg-deep";
  return (
    <div className="h-2 overflow-hidden rounded-full bg-bg" role="progressbar" aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100} aria-label={`${pct}%`}>
      <div className={`h-full ${color} transition-[width] duration-700`} style={{ width: `${pct}%` }} />
    </div>
  );
}

function titleLink(d: Download) {
  if (!d.titleType || !d.titleId) return null;
  const [imdb] = d.titleId.split(":");
  const ep = d.titleType === "series" ? `?ep=${encodeURIComponent(d.titleId)}&s=${d.titleId.split(":")[1] ?? ""}` : "";
  return `/title/${d.titleType}/${imdb}${ep}`;
}

function Row({ d }: { d: Download }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [removing, setRemoving] = useState(false);
  const [deleteData, setDeleteData] = useState(true);
  const link = titleLink(d);
  const running = d.state === "downloading" || d.state === "metadata" || d.state === "queued" || d.state === "seeding";

  async function act(fn: () => Promise<unknown>) {
    setBusy(true);
    setError("");
    try {
      await fn();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Something went wrong.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <li className="flex flex-col gap-2 px-4 py-3">
      <div className="flex flex-col gap-1 sm:flex-row sm:items-start sm:justify-between sm:gap-4">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            {link ? (
              <Link to={link} className="font-medium hover:underline">{d.titleName || d.name}</Link>
            ) : (
              <span className="font-medium">{d.titleName || d.name}</span>
            )}
            {d.titleId?.includes(":") && <span className="font-mono text-xs text-muted">{episodeOf(d.titleId)}</span>}
            <StateChip state={d.state} />
          </div>
          <p className="truncate font-mono text-xs text-muted" title={d.filePath || d.name}>{d.filePath ? d.filePath.split(/[\\/]/).slice(1).join("/") : d.name}</p>
        </div>
        <div className="flex shrink-0 gap-4 text-sm tabular-nums text-muted">
          <span title="Download speed">↓ {rate(d.downRate)}</span>
          <span title="Upload speed">↑ {rate(d.upRate)}</span>
          <span title="Connected peers (seeds)">{d.peers} peers{d.seeds ? ` (${d.seeds})` : ""}</span>
        </div>
      </div>
      <ProgressBar d={d} />
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 text-xs text-muted tabular-nums">
        <span>{bytes(d.doneBytes)} of {d.size ? bytes(d.size) : "?"} · {Math.floor(d.progress * 100)}%</span>
        {d.state === "downloading" && <span>{d.eta >= 0 ? `${duration(d.eta)} left` : "waiting for speed"}</span>}
        {d.state === "seeding" && <span>Shared {bytes(d.uploaded)} · ratio {d.ratio.toFixed(2)}</span>}
        {d.error && <span className="text-bad">{d.error}</span>}
        {error && <span role="alert" className="text-bad">{error}</span>}
        <span className="ml-auto flex items-center gap-2">
          {running ? (
            <button className="btn-ghost py-1" disabled={busy} onClick={() => act(() => api.pauseDownload(d.id))}>{d.state === "seeding" ? "Stop sharing" : "Pause"}</button>
          ) : (
            <button className="btn-ghost py-1" disabled={busy} onClick={() => act(() => api.resumeDownload(d.id))}>{d.state === "failed" ? "Retry" : "Resume"}</button>
          )}
          {removing ? (
            <span className="flex flex-wrap items-center gap-2 text-ink">
              <label className="flex items-center gap-1.5">
                <input id={`del-data-${d.id}`} type="checkbox" checked={deleteData} onChange={(e) => setDeleteData(e.target.checked)} className="accent-[var(--hb-bad)]" />
                Delete the file too
              </label>
              <button className="btn-danger py-1" disabled={busy} onClick={() => act(() => api.removeDownload(d.id, deleteData))}>Remove</button>
              <button className="btn-ghost py-1" onClick={() => setRemoving(false)}>Keep</button>
            </span>
          ) : (
            <button className="btn-ghost py-1 text-bad" disabled={busy} onClick={() => setRemoving(true)}>Remove</button>
          )}
        </span>
      </div>
    </li>
  );
}

function episodeOf(id: string) {
  const [, s, e] = id.split(":");
  return `S${s.padStart(2, "0")}E${e.padStart(2, "0")}`;
}

export default function Downloads() {
  const { downloads, error } = useDownloads();
  const active = downloads?.filter((d) => d.state !== "seeding") ?? [];
  const done = downloads?.filter((d) => d.state === "seeding") ?? [];
  const down = downloads?.reduce((a, d) => a + d.downRate, 0) ?? 0;
  const up = downloads?.reduce((a, d) => a + d.upRate, 0) ?? 0;

  return (
    <div className="flex flex-col gap-6">
      <header className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <p className="eyebrow">Downloads</p>
          <h1 className="mt-1 text-2xl font-semibold tracking-tight">Downloads</h1>
        </div>
        <p className="text-sm tabular-nums text-muted">↓ {rate(down)} · ↑ {rate(up)}</p>
      </header>
      {error && <p role="alert" className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">{error}</p>}
      {!downloads && !error && <p className="text-muted">Loading…</p>}
      {downloads && downloads.length === 0 && (
        <div className="card border-dashed p-6 text-muted">
          Nothing here yet. Open a title from <Link to="/search" className="text-deep underline">Search</Link> and press Download on one of its streams.
        </div>
      )}
      {active.length > 0 && (
        <section className="flex flex-col gap-2">
          <h2 className="font-semibold">In progress</h2>
          <ul className="card divide-y divide-line">{active.map((d) => <Row key={d.id} d={d} />)}</ul>
        </section>
      )}
      {done.length > 0 && (
        <section className="flex flex-col gap-2">
          <h2 className="font-semibold">Finished</h2>
          <p className="text-sm text-muted">Finished files keep being shared with other peers. Automatic stop rules arrive in M5; until then, stop or remove them here.</p>
          <ul className="card divide-y divide-line">{done.map((d) => <Row key={d.id} d={d} />)}</ul>
        </section>
      )}
    </div>
  );
}
