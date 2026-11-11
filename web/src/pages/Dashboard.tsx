import { useEffect, useState } from "react";
import { Link } from "react-router";
import { api, type SystemInfo } from "../api";

function uptime(sec: number) {
  const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60);
  return d ? `${d}d ${h}h` : h ? `${h}h ${m}m` : `${m}m`;
}

const STEPS = [
  { to: "/suppliers", title: "Add a supplier", text: "Paste a Stremio addon manifest URL that returns torrent streams.", milestone: "M1" },
  { to: "/profiles", title: "Set a quality profile", text: "Pick screen sizes, languages and subtitles you want.", milestone: "M4" },
  { to: "/titles", title: "Track a title", text: "Search a movie or series and Harbor fetches it for you.", milestone: "M4" },
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
        <h1 className="mt-1 text-2xl font-semibold tracking-tight">Nothing tracked yet</h1>
        <p className="mt-1 text-muted">Harbor is running. Downloads and recent imports will appear here.</p>
      </header>

      <section>
        <h2 className="mb-3 font-semibold">Getting started</h2>
        <ol className="grid gap-3 sm:grid-cols-3">
          {STEPS.map((s, i) => (
            <li key={s.to}>
              <Link to={s.to} className="card flex h-full flex-col gap-1 p-4 transition-colors hover:border-deep">
                <span className="font-mono text-xs text-muted">Step {i + 1} · available in {s.milestone}</span>
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
