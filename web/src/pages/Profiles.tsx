import { useEffect, useState, type FormEvent } from "react";
import { api, type Language, type Profile } from "../api";

const BLANK: Partial<Profile> = {
  name: "",
  resolutions: ["1080p", "720p"],
  movieMinMB: 700,
  movieMaxMB: 20000,
  episodeMinMB: 150,
  episodeMaxMB: 6000,
  audioLangs: [],
  audioRequired: false,
  subtitleLangs: ["en"],
  blockedTerms: ["CAM", "TS", "TELESYNC", "SCREENER"],
  preferredTerms: [],
  minSeeders: 3,
  seedRatio: 1,
  seedHours: 48,
};

function LangPicker({ id, value, all, onChange }: { id: string; value: string[]; all: Language[]; onChange: (v: string[]) => void }) {
  const name = (c: string) => all.find((l) => l.code === c)?.name ?? c;
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {value.map((l) => (
        <span key={l} className="flex items-center gap-1 rounded-full bg-deep-soft py-0.5 pr-1 pl-2.5 text-sm text-deep">
          {name(l)}
          <button type="button" className="grid h-5 w-5 place-items-center rounded-full hover:bg-surface" aria-label={`Remove ${name(l)}`} onClick={() => onChange(value.filter((x) => x !== l))}>×</button>
        </span>
      ))}
      <label htmlFor={id} className="sr-only">Add a language</label>
      <select id={id} className="input w-auto py-1" value="" onChange={(e) => e.target.value && onChange([...value, e.target.value])}>
        <option value="">Add…</option>
        {all.filter((l) => !value.includes(l.code)).map((l) => <option key={l.code} value={l.code}>{l.name}</option>)}
      </select>
    </div>
  );
}

function Editor({ start, resolutions, langs, onDone }: { start: Partial<Profile>; resolutions: string[]; langs: Language[]; onDone: (saved?: Profile) => void }) {
  const [p, setP] = useState<Partial<Profile>>(start);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const set = <K extends keyof Profile>(k: K, v: Profile[K]) => setP((x) => ({ ...x, [k]: v }));
  const num = (k: keyof Profile) => (e: React.ChangeEvent<HTMLInputElement>) => set(k, Math.max(0, Number(e.target.value) || 0) as never);
  const terms = (k: "blockedTerms" | "preferredTerms") => (e: React.ChangeEvent<HTMLInputElement>) => set(k, e.target.value.split(",").map((t) => t.trim()).filter(Boolean));

  function toggleRes(r: string) {
    const cur = p.resolutions ?? [];
    set("resolutions", cur.includes(r) ? cur.filter((x) => x !== r) : resolutions.filter((x) => cur.includes(x) || x === r));
  }

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      onDone(await api.saveProfile(p));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Couldn't save.");
    } finally {
      setBusy(false);
    }
  }

  const sizeField = (k: keyof Profile, label: string) => (
    <div>
      <label className="label" htmlFor={`pf-${k}`}>{label}</label>
      <input id={`pf-${k}`} type="number" min={0} className="input" value={Number(p[k] ?? 0)} onChange={num(k)} />
    </div>
  );

  return (
    <form onSubmit={save} className="card flex flex-col gap-5 p-5">
      <div>
        <label className="label" htmlFor="pf-name">Name</label>
        <input id="pf-name" className="input sm:max-w-sm" value={p.name ?? ""} onChange={(e) => set("name", e.target.value)} autoFocus />
      </div>
      <fieldset className="flex flex-col gap-2">
        <legend className="label">Screen sizes it may download</legend>
        <p className="text-xs text-muted">Larger sizes are preferred, in this order: {(p.resolutions ?? []).join(" › ") || "none chosen"}.</p>
        <div className="flex flex-wrap gap-3">
          {resolutions.map((r) => (
            <label key={r} className="flex items-center gap-1.5 text-sm">
              <input id={`pf-res-${r}`} type="checkbox" checked={p.resolutions?.includes(r) ?? false} onChange={() => toggleRes(r)} className="accent-[var(--hb-deep)]" />
              {r}
            </label>
          ))}
        </div>
      </fieldset>
      <div className="grid gap-4 sm:grid-cols-4">
        {sizeField("movieMinMB", "Movie min (MB)")}
        {sizeField("movieMaxMB", "Movie max (MB)")}
        {sizeField("episodeMinMB", "Episode min (MB)")}
        {sizeField("episodeMaxMB", "Episode max (MB)")}
      </div>
      <p className="-mt-3 text-xs text-muted">0 = no limit. Season packs aren't held to the episode limits, since addons often report the whole pack's size.</p>
      <div className="flex flex-col gap-2">
        <span className="label">Audio languages (preferred first)</span>
        <LangPicker id="pf-audio" value={p.audioLangs ?? []} all={langs} onChange={(v) => set("audioLangs", v)} />
        <label className="flex items-center gap-2 text-sm">
          <input id="pf-audio-req" type="checkbox" checked={p.audioRequired ?? false} onChange={(e) => set("audioRequired", e.target.checked)} className="accent-[var(--hb-deep)]" />
          Required: skip releases without one of these, and don't import a file whose audio turns out different
        </label>
        <p className="text-xs text-muted">Releases that don't name a language are taken to be English.</p>
      </div>
      <div className="flex flex-col gap-2">
        <span className="label">Subtitle languages</span>
        <LangPicker id="pf-subs" value={p.subtitleLangs ?? []} all={langs} onChange={(v) => set("subtitleLangs", v)} />
      </div>
      <div className="grid gap-4 sm:grid-cols-2">
        <div>
          <label className="label" htmlFor="pf-blocked">Never download releases with these words</label>
          <input id="pf-blocked" className="input" defaultValue={(p.blockedTerms ?? []).join(", ")} onChange={terms("blockedTerms")} placeholder="CAM, TS, 3D" />
        </div>
        <div>
          <label className="label" htmlFor="pf-preferred">Prefer releases with these words</label>
          <input id="pf-preferred" className="input" defaultValue={(p.preferredTerms ?? []).join(", ")} onChange={terms("preferredTerms")} placeholder="REMUX, x265, HDR" />
        </div>
      </div>
      <div className="grid gap-4 sm:grid-cols-3">
        {sizeField("minSeeders", "Minimum seeders")}
        <div>
          <label className="label" htmlFor="pf-ratio">Stop sharing at ratio</label>
          <input id="pf-ratio" type="number" min={0} step={0.1} className="input" value={p.seedRatio ?? 0} onChange={(e) => set("seedRatio", Math.max(0, Number(e.target.value) || 0))} />
        </div>
        {sizeField("seedHours", "…or after hours")}
      </div>
      <p className="-mt-3 text-xs text-muted">Sharing stops at whichever limit comes first. The download copy is then deleted; the library file stays.</p>
      {error && <p role="alert" className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">{error}</p>}
      <div className="flex gap-2">
        <button className="btn-primary" disabled={busy}>{p.id ? "Save profile" : "Create profile"}</button>
        <button type="button" className="btn-ghost" onClick={() => onDone()}>Cancel</button>
      </div>
    </form>
  );
}

export default function Profiles() {
  const [list, setList] = useState<Profile[] | null>(null);
  const [resolutions, setResolutions] = useState<string[]>([]);
  const [langs, setLangs] = useState<Language[]>([]);
  const [editing, setEditing] = useState<Partial<Profile> | null>(null);
  const [error, setError] = useState("");

  const load = () => api.profiles().then((r) => (setList(r.profiles), setResolutions(r.resolutions))).catch((e) => setError(e.message));
  useEffect(() => {
    load();
    api.languages().then(setLangs).catch(() => {});
  }, []);

  async function remove(p: Profile) {
    setError("");
    try {
      await api.deleteProfile(p.id);
      load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't delete.");
    }
  }

  const lname = (c: string) => langs.find((l) => l.code === c)?.name ?? c;

  return (
    <div className="flex flex-col gap-6">
      <header className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <p className="eyebrow">Quality profiles</p>
          <h1 className="mt-1 text-2xl font-semibold tracking-tight">What to download</h1>
          <p className="mt-1 max-w-prose text-muted">Each tracked title uses a profile to decide which release to grab: screen size, file size, languages, and words to avoid or prefer.</p>
        </div>
        {!editing && <button className="btn-primary" onClick={() => setEditing({ ...BLANK })}>New profile</button>}
      </header>
      {error && <p role="alert" className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">{error}</p>}
      {editing && <Editor key={editing.id ?? "new"} start={editing} resolutions={resolutions} langs={langs} onDone={() => (setEditing(null), load())} />}
      {!list && !error && <p className="text-muted">Loading…</p>}
      {list && (
        <ul className="grid gap-3 sm:grid-cols-2">
          {list.map((p) => (
            <li key={p.id} className="card flex flex-col gap-2 p-4">
              <div className="flex items-start justify-between gap-2">
                <h2 className="font-semibold">{p.name}</h2>
                <span className="flex gap-1.5">
                  <button className="btn-ghost py-1" onClick={() => setEditing(p)}>Edit</button>
                  <button className="btn-ghost py-1 text-bad" onClick={() => remove(p)}>Delete</button>
                </span>
              </div>
              <dl className="grid grid-cols-[max-content_1fr] gap-x-3 gap-y-1 text-sm">
                <dt className="text-muted">Sizes</dt>
                <dd>{p.resolutions.join(" › ")}</dd>
                <dt className="text-muted">Movie</dt>
                <dd className="tabular-nums">{p.movieMinMB}–{p.movieMaxMB || "∞"} MB</dd>
                <dt className="text-muted">Episode</dt>
                <dd className="tabular-nums">{p.episodeMinMB}–{p.episodeMaxMB || "∞"} MB</dd>
                <dt className="text-muted">Audio</dt>
                <dd>{p.audioLangs.length ? `${p.audioLangs.map(lname).join(", ")}${p.audioRequired ? " (required)" : ""}` : "any"}</dd>
                <dt className="text-muted">Subtitles</dt>
                <dd>{p.subtitleLangs.length ? p.subtitleLangs.map(lname).join(", ") : "none"}</dd>
                <dt className="text-muted">Seeders</dt>
                <dd>at least {p.minSeeders}</dd>
              </dl>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
