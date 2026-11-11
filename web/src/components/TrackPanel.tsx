import { useEffect, useState } from "react";
import { api, ApiError, type Item, type Profile, type Tracked } from "../api";

const MONITOR = [
  { v: "all", label: "All episodes" },
  { v: "future", label: "Only new episodes" },
  { v: "none", label: "None (paused)" },
];

// TrackPanel shows tracking controls for a title and reports the tracked
// state (and its items) to the page.
export default function TrackPanel({
  imdbId,
  type,
  onChange,
}: {
  imdbId: string;
  type: "movie" | "series";
  onChange: (t: Tracked | null, items: Item[], profiles: Profile[]) => void;
}) {
  const [tracked, setTracked] = useState<Tracked | null | undefined>(undefined);
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [profileId, setProfileId] = useState(0);
  const [monitor, setMonitor] = useState("all");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [confirmStop, setConfirmStop] = useState(false);
  const [msg, setMsg] = useState("");

  function apply(t: Tracked | null, items: Item[], ps = profiles) {
    setTracked(t);
    if (t) {
      setProfileId(t.profileId);
      setMonitor(t.monitor);
    }
    onChange(t, items, ps);
  }

  useEffect(() => {
    let live = true;
    Promise.all([api.profiles(), api.trackedByImdb(imdbId).catch((e) => (e instanceof ApiError && e.status === 404 ? null : Promise.reject(e)))])
      .then(([p, t]) => {
        if (!live) return;
        setProfiles(p.profiles);
        if (!t) {
          setProfileId(p.profiles[0]?.id ?? 0);
          apply(null, [], p.profiles);
        } else apply(t.title, t.items, p.profiles);
      })
      .catch((e) => live && setError(e.message));
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [imdbId]);

  async function run(fn: () => Promise<void>) {
    setBusy(true);
    setError("");
    setMsg("");
    try {
      await fn();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Something went wrong.");
    } finally {
      setBusy(false);
    }
  }

  if (tracked === undefined) return error ? <p role="alert" className="text-sm text-bad">{error}</p> : null;

  const profileSelect = (
    <>
      <label htmlFor="track-profile" className="sr-only">Quality profile</label>
      <select id="track-profile" className="input w-auto py-1.5" value={profileId} onChange={(e) => setProfileId(Number(e.target.value))}>
        {profiles.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
      </select>
    </>
  );
  const monitorSelect = type === "series" && (
    <>
      <label htmlFor="track-monitor" className="sr-only">Episodes to get</label>
      <select id="track-monitor" className="input w-auto py-1.5" value={monitor} onChange={(e) => setMonitor(e.target.value)}>
        {MONITOR.map((m) => <option key={m.v} value={m.v}>{m.label}</option>)}
      </select>
    </>
  );

  return (
    <div className="card flex flex-col gap-3 p-4">
      {!tracked ? (
        <>
          <p className="text-sm">
            <span className="font-medium">Track this {type === "movie" ? "movie" : "series"}</span>
            <span className="text-muted"> and Harbor will download {type === "movie" ? "it" : "episodes"} by itself when a release fits the profile.</span>
          </p>
          <div className="flex flex-wrap items-center gap-2">
            {profileSelect}
            {monitorSelect}
            <button
              className="btn-primary py-1.5"
              disabled={busy || !profileId}
              onClick={() => run(async () => { const r = await api.track({ imdbId, type, profileId, monitor }); apply(r.title, r.items); })}
            >
              {busy ? "Starting…" : "Track"}
            </button>
          </div>
        </>
      ) : (
        <>
          <p className="text-sm">
            <span className="rounded-full bg-ok-soft px-2 py-0.5 text-xs text-ok">Tracked</span>{" "}
            {type === "movie"
              ? tracked.imported ? "In your library." : tracked.downloading ? "Downloading." : "Harbor keeps looking for a release that fits."
              : `${tracked.imported} in library · ${tracked.downloading} downloading · ${tracked.wanted} wanted · ${tracked.unaired} not aired yet`}
          </p>
          <div className="flex flex-wrap items-center gap-2">
            {profileSelect}
            {monitorSelect}
            {(profileId !== tracked.profileId || monitor !== tracked.monitor) && (
              <button
                className="btn-primary py-1.5"
                disabled={busy}
                onClick={() => run(async () => { const r = await api.updateTracked(tracked.id, { profileId, monitor, seasons: tracked.seasons }); apply(r.title, r.items); setMsg("Saved."); })}
              >
                Save
              </button>
            )}
            <button className="btn-ghost py-1.5" disabled={busy || tracked.wanted === 0} onClick={() => run(async () => { await api.searchTitleNow(tracked.id); setMsg("Searching now; results show up below in a moment."); })}>
              Search now
            </button>
            {confirmStop ? (
              <span className="flex items-center gap-2 text-sm">
                Stop tracking? Files and downloads stay.
                <button className="btn-danger py-1" disabled={busy} onClick={() => run(async () => { await api.untrack(tracked.id); setConfirmStop(false); apply(null, []); })}>Stop</button>
                <button className="btn-ghost py-1" onClick={() => setConfirmStop(false)}>Keep</button>
              </span>
            ) : (
              <button className="btn-ghost py-1.5 text-bad" onClick={() => setConfirmStop(true)}>Stop tracking</button>
            )}
          </div>
        </>
      )}
      {msg && <p role="status" className="text-sm text-ok">{msg}</p>}
      {error && <p role="alert" className="text-sm text-bad">{error}</p>}
    </div>
  );
}
