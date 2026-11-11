import { useEffect, useState } from "react";
import { api, type MediaFile, type SubtitleResult } from "../api";
import { bytes, episodeCode, langName } from "../format";

const RESULT_TEXT: Record<SubtitleResult["status"], string> = {
  embedded: "in the video",
  present: "already saved",
  saved: "saved",
  none: "none found",
  failed: "download failed",
};

function FileRow({ f, onChange }: { f: MediaFile; onChange: (f: MediaFile) => void }) {
  const [busy, setBusy] = useState(false);
  const [results, setResults] = useState<SubtitleResult[] | null>(null);
  const [error, setError] = useState("");
  const [, s, e] = f.titleId.split(":");
  const parts = f.path.split("/");

  async function findSubs() {
    setBusy(true);
    setError("");
    try {
      const r = await api.fetchSubtitles(f.id);
      setResults(r.results);
      onChange(r.file);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Couldn't look for subtitles.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <li className="flex flex-col gap-2 px-4 py-3">
      <div className="flex flex-col gap-1 sm:flex-row sm:items-start sm:justify-between sm:gap-4">
        <div className="min-w-0 flex-1">
          <p className="text-sm font-medium">
            {s && e ? <span className="mr-2 font-mono text-xs text-muted">{episodeCode(Number(s), Number(e))}</span> : null}
            {parts[parts.length - 1]}
          </p>
          <p className="truncate font-mono text-xs text-muted" title={f.path}>{parts.slice(0, -1).join("/")}/</p>
        </div>
        <span className="shrink-0 text-xs tabular-nums text-muted">
          {[f.resolution, f.videoCodec, bytes(f.size), f.method === "hardlink" ? "linked (no extra space)" : "copied"].filter(Boolean).join(" · ")}
        </span>
      </div>
      <div className="flex flex-wrap items-center gap-1.5 text-xs">
        {f.audioLangs.length > 0 && <span className="text-muted">Audio: {f.audioLangs.map(langName).join(", ")}</span>}
        <span className="text-muted">Subtitles:</span>
        {f.embeddedSubs.map((l) => (
          <span key={"e" + l} className="rounded bg-bg px-1.5 py-0.5" title="Inside the video file">{langName(l)} (built in)</span>
        ))}
        {f.subtitles.map((sub) => (
          <span key={sub.id} className="rounded bg-ok-soft px-1.5 py-0.5 text-ok" title={`${sub.path} · from ${sub.source}`}>{langName(sub.lang)}</span>
        ))}
        {f.embeddedSubs.length === 0 && f.subtitles.length === 0 && <span className="text-muted">none yet</span>}
        <button className="btn-ghost ml-auto py-1" disabled={busy} onClick={findSubs}>{busy ? "Looking…" : "Find subtitles"}</button>
      </div>
      {results && (
        <p className="text-xs text-muted">
          {results.length === 0 ? "No subtitle languages chosen in Settings." : results.map((r) => `${langName(r.lang)}: ${RESULT_TEXT[r.status]}${r.error ? ` (${r.error})` : ""}`).join(" · ")}
        </p>
      )}
      {error && <p role="alert" className="text-xs text-bad">{error}</p>}
    </li>
  );
}

// LibraryFiles lists what's in the library for a title (all episodes when
// given a series id) or for one episode.
export default function LibraryFiles({ titleId, episodeId }: { titleId: string; episodeId?: string }) {
  const [files, setFiles] = useState<MediaFile[] | null>(null);

  useEffect(() => {
    let live = true;
    api.library(titleId).then((r) => live && setFiles(r.files)).catch(() => live && setFiles([]));
    return () => {
      live = false;
    };
  }, [titleId]);

  if (!files) return null;
  const shown = episodeId ? files.filter((f) => f.titleId === episodeId) : files;
  if (shown.length === 0) return null;
  const replace = (nf: MediaFile) => setFiles((fs) => fs?.map((f) => (f.id === nf.id ? nf : f)) ?? null);

  return (
    <section className="flex flex-col gap-2">
      <h3 className="font-semibold">In your library</h3>
      <ul className="card divide-y divide-line">{shown.map((f) => <FileRow key={f.id} f={f} onChange={replace} />)}</ul>
    </section>
  );
}
