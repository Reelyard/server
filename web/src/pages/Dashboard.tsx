import { useEffect, useState } from "react";
import { Link } from "react-router";
import { api, type SystemInfo } from "../api";
import { rate } from "../format";
import { useDownloads } from "../useDownloads";
import { ProgressBar, StateChip } from "./Downloads";

function uptime(sec: number) {
  const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60);
  return d ? `${d}d ${h}h` : h ? `${h}h ${m}m` : `${m}m`;
}

const STEPS = [
  { to: "/suppliers", title: "Add a supplier", text: "Paste a Stremio addon manifest URL that returns torrent streams.", milestone: "" },
  { to: "/search", title: "Find a title", text: "Search a movie or series and see which torrents your suppliers have.", milestone: "" },
  { to: "/titles", title: "Track it", text: "Press Track on a title; Reelyard downloads what fits its quality profile.", milestone: "" },
];

export default function Dashboard() {
  const [info, setInfo] = useState<SystemInfo | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    api.system().then(setInfo).catch((e) => setError(e.message));
  }, []);

  return (
    <div className="flex flex-col gap-8">
      <header>
        <p className="eyebrow">Dashboard</p>
        <h1 className="mt-1 text-2xl font-semibold tracking-tight">Reelyard</h1>
        <p className="mt-1 text-muted">Your downloads at a glance.</p>
      </header>

      <DownloadsWidget />

      <section>
        <h2 className="mb-3 font-semibold">Getting started</h2>
        <ol className="grid gap-3 sm:grid-cols-3">
          {STEPS.map((s, i) => (
            <li key={s.to}>
              <Link to={s.to} className="card flex h-full flex-col gap-1 p-4 transition-colors hover:border-deep">
                <span className="font-mono text-xs text-muted">Step {i + 1}{s.milestone ? ` · available in ${s.milestone}` : ""}</span>
                <span className="font-medium">{s.title}</span>
                <span className="text-sm text-muted">{s.text}</span>
              </Link>
            </li>
          ))}
        </ol>
      </section>

      <section>
        <h2 className="mb-3 font-semibold">This server</h2>
        {error && <p className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">{error}</p>}
        {!info && !error && <p className="text-muted">Loading…</p>}
        {info && (
          <dl className="card grid gap-x-6 gap-y-3 p-5 text-sm sm:grid-cols-[max-content_1fr]">
            <dt className="text-muted">Version</dt>
            <dd className="font-mono">{info.version} <span className="text-muted">({info.commit.slice(0, 7)}, {info.os}/{info.arch}, {info.goVersion})</span></dd>
            <dt className="text-muted">Uptime</dt>
            <dd className="tabular-nums">{uptime(info.uptimeSec)}</dd>
            <dt className="text-muted">Library folder</dt>
            <dd className="font-mono break-all">{info.libraryDir}</dd>
            <dt className="text-muted">Download folder</dt>
            <dd className="font-mono break-all">{info.downloadDir}</dd>
            <dt className="text-muted">Data folder</dt>
            <dd className="font-mono break-all">{info.dataDir}</dd>
            <dt className="text-muted">Config file</dt>
            <dd className="font-mono break-all">{info.configFile || <span className="text-muted">none (built-in defaults)</span>}</dd>
            <dt className="text-muted">Database schema</dt>
            <dd className="tabular-nums">v{info.schema}</dd>
          </dl>
        )}
      </section>
    </div>
  );
}

function DownloadsWidget() {
  const { downloads } = useDownloads();
  if (!downloads || downloads.length === 0) return null;
  const active = downloads.filter((d) => d.state !== "seeding" && d.state !== "paused");
  const down = downloads.reduce((a, d) => a + d.downRate, 0);
  const up = downloads.reduce((a, d) => a + d.upRate, 0);
  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="font-semibold">Downloads</h2>
        <span className="text-sm tabular-nums text-muted">
          {active.length} active · ↓ {rate(down)} · ↑ {rate(up)} · <Link to="/downloads" className="text-deep hover:underline">See all</Link>
        </span>
      </div>
      <ul className="card divide-y divide-line">
        {(active.length ? active : downloads).slice(0, 4).map((d) => (
          <li key={d.id} className="flex flex-col gap-1.5 px-4 py-3">
            <div className="flex items-center justify-between gap-3 text-sm">
              <span className="min-w-0 truncate font-medium">{d.titleName || d.name}</span>
              <span className="flex shrink-0 items-center gap-2 tabular-nums text-muted">
                {Math.floor(d.progress * 100)}% <StateChip state={d.state} />
              </span>
            </div>
            <ProgressBar d={d} />
          </li>
        ))}
      </ul>
    </section>
  );
}
