import { useEffect, useMemo, useState, type FormEvent } from "react";
import { api, type DlnaServer, type TV, type Tracked, type Viewer, type ViewerInput } from "../api";
import { Link } from "react-router";
import { ago } from "../format";

type Msg = { ok: boolean; text: string } | null;

function Note({ msg }: { msg: Msg }) {
  if (!msg) return null;
  return <p role="status" className={`rounded-lg px-3 py-2 text-sm ${msg.ok ? "bg-ok-soft text-ok" : "bg-bad-soft text-bad"}`}>{msg.text}</p>;
}

const errText = (e: unknown, fallback: string) => (e instanceof Error ? e.message : fallback);
const tvLabel = (tv: TV) => tv.name || tv.model || tv.address;

// TvPicker chooses which TVs a DLNA server shows on. None ticked means
// every TV.
export function TvPicker({ value, onChange, idPrefix }: { value: string[]; onChange: (tvs: string[]) => void; idPrefix: string }) {
  const [tvs, setTvs] = useState<TV[] | null>(null);
  const [editing, setEditing] = useState<string | null>(null);
  const [name, setName] = useState("");

  useEffect(() => {
    api.tvs().then(setTvs).catch(() => setTvs([]));
  }, []);

  const toggle = (a: string) => onChange(value.includes(a) ? value.filter((x) => x !== a) : [...value, a]);
  async function rename(a: string) {
    try {
      setTvs(await api.nameTV(a, name));
    } finally {
      setEditing(null);
    }
  }

  const known = tvs ?? [];
  const missing = tvs === null ? [] : value.filter((a) => !known.some((t) => t.address === a));
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="label">Show on these TVs</legend>
      <p className="text-xs text-muted">
        {value.length === 0 ? "Shown on every TV. Tick some to show it only there." : "Shown only on the ticked TVs; others can't open it."} A TV appears here after it has opened any Reelyard server once.
        TVs are told apart by address, so give each a fixed IP in your router.
      </p>
      {tvs === null && <span className="text-sm text-muted">Loading…</span>}
      {tvs !== null && known.length === 0 && missing.length === 0 && <span className="text-sm text-muted">No TVs yet. Open Reelyard on a TV, then come back.</span>}
      <div className="flex flex-col gap-1.5">
        {known.map((tv) => (
          <div key={tv.address} className="flex flex-wrap items-center gap-2 text-sm">
            <input id={`${idPrefix}-tv-${tv.address}`} type="checkbox" checked={value.includes(tv.address)} onChange={() => toggle(tv.address)} className="accent-[var(--hb-deep)]" />
            {editing === tv.address ? (
              <>
                <input className="input w-48 py-1" autoFocus maxLength={64} value={name} placeholder={tv.model || tv.address} onChange={(e) => setName(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
                      rename(tv.address);
                    } else if (e.key === "Escape") setEditing(null);
                  }} />
                <button type="button" className="btn-ghost py-1" onClick={() => rename(tv.address)}>Save</button>
              </>
            ) : (
              <>
                <label htmlFor={`${idPrefix}-tv-${tv.address}`}>{tvLabel(tv)}</label>
                <button type="button" className="text-xs text-deep underline" onClick={() => { setEditing(tv.address); setName(tv.name ?? ""); }}>Rename</button>
              </>
            )}
            <span className="font-mono text-xs text-muted">{tv.address}</span>
            <span className="text-xs text-muted">seen {ago(tv.lastSeen)}</span>
          </div>
        ))}
        {missing.map((a) => (
          <label key={a} className="flex items-center gap-2 text-sm text-muted">
            <input type="checkbox" checked onChange={() => toggle(a)} className="accent-[var(--hb-deep)]" />
            <span className="font-mono">{a}</span> (not seen lately)
          </label>
        ))}
      </div>
    </fieldset>
  );
}

function TitlePicker({ titles, value, onChange, idPrefix }: { titles: Tracked[] | null; value: string[]; onChange: (ids: string[]) => void; idPrefix: string }) {
  const [q, setQ] = useState("");
  const shown = useMemo(() => {
    const list = [...(titles ?? [])].sort((a, b) => a.name.localeCompare(b.name));
    const f = q.trim().toLowerCase();
    return f ? list.filter((t) => t.name.toLowerCase().includes(f)) : list;
  }, [titles, q]);
  const toggle = (id: string) => onChange(value.includes(id) ? value.filter((x) => x !== id) : [...value, id]);
  if (titles === null) return <span className="text-sm text-muted">Loading titles…</span>;
  if (titles.length === 0) return <span className="text-sm text-muted">No titles yet. Track some first.</span>;
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <input className="input max-w-xs py-1" placeholder="Filter titles" value={q} onChange={(e) => setQ(e.target.value)} aria-label="Filter titles" />
        <span className="text-xs text-muted">{value.length} chosen</span>
      </div>
      <div className="grid max-h-64 gap-1 overflow-y-auto rounded-lg bg-bg p-2 sm:grid-cols-2">
        {shown.map((t) => (
          <label key={t.imdbId} className="flex items-center gap-2 text-sm">
            <input id={`${idPrefix}-t-${t.imdbId}`} type="checkbox" checked={value.includes(t.imdbId)} onChange={() => toggle(t.imdbId)} className="accent-[var(--hb-deep)]" />
            <span className="truncate">{t.name}</span>
            <span className="shrink-0 text-xs text-muted">{t.year ?? ""} {t.type === "series" ? "· series" : ""}</span>
          </label>
        ))}
      </div>
    </div>
  );
}

const blank: ViewerInput = { name: "", allTitles: true, titles: [], dlna: true, tvs: [] };

function ViewerEditor({ start, id, titles, onDone }: { start: ViewerInput; id?: number; titles: Tracked[] | null; onDone: (changed: boolean) => void }) {
  const [v, setV] = useState<ViewerInput>(start);
  const [msg, setMsg] = useState<Msg>(null);
  const [busy, setBusy] = useState(false);
  const set = (patch: Partial<ViewerInput>) => setV((x) => ({ ...x, ...patch }));
  const prefix = `viewer-${id ?? "new"}`;

  async function save(e: FormEvent) {
    e.preventDefault();
    if (!v.name.trim()) {
      setMsg({ ok: false, text: "Give the viewer a name." });
      return;
    }
    setBusy(true);
    setMsg(null);
    try {
      await api.saveViewer(v, id);
      onDone(true);
    } catch (err) {
      setMsg({ ok: false, text: errText(err, "Couldn't save.") });
      setBusy(false);
    }
  }

  return (
    <form onSubmit={save} className="flex flex-col gap-4 rounded-xl border border-line p-4">
      <div className="max-w-xs">
        <label className="label" htmlFor={`${prefix}-name`}>Name</label>
        <input id={`${prefix}-name`} className="input" maxLength={32} value={v.name} placeholder="Kids" onChange={(e) => set({ name: e.target.value })} />
      </div>
      <fieldset className="flex flex-col gap-2">
        <legend className="label">What they can see</legend>
        <label className="flex items-center gap-2 text-sm">
          <input type="radio" name={`${prefix}-see`} checked={v.allTitles} onChange={() => set({ allTitles: true })} className="accent-[var(--hb-deep)]" />
          The whole library
        </label>
        <label className="flex items-center gap-2 text-sm">
          <input type="radio" name={`${prefix}-see`} checked={!v.allTitles} onChange={() => set({ allTitles: false })} className="accent-[var(--hb-deep)]" />
          Only titles I choose
        </label>
        {!v.allTitles && <TitlePicker titles={titles} value={v.titles} onChange={(titles) => set({ titles })} idPrefix={prefix} />}
      </fieldset>
      <label className="flex items-start gap-2 text-sm">
        <input id={`${prefix}-dlna`} type="checkbox" className="mt-1 accent-[var(--hb-deep)]" checked={v.dlna} onChange={(e) => set({ dlna: e.target.checked })} />
        <span>
          Their own server on TVs
          <span className="block text-xs text-muted">Shown in the TV's sources as a separate Reelyard server, with their titles, watched marks and resume points.</span>
        </span>
      </label>
      {v.dlna && <TvPicker value={v.tvs} onChange={(tvs) => set({ tvs })} idPrefix={prefix} />}
      <Note msg={msg} />
      <div className="flex gap-2">
        <button className="btn-primary" disabled={busy}>{id ? "Save viewer" : "Add viewer"}</button>
        <button type="button" className="btn-ghost" onClick={() => onDone(false)}>Cancel</button>
      </div>
    </form>
  );
}

// Viewers are family members with their own DLNA server, titles and resume
// points.
export function Viewers() {
  const [viewers, setViewers] = useState<Viewer[] | null>(null);
  const [servers, setServers] = useState<DlnaServer[]>([]);
  const [dlnaOn, setDlnaOn] = useState(true);
  const [titles, setTitles] = useState<Tracked[] | null>(null);
  const [editing, setEditing] = useState<number | "new" | null>(null);
  const [msg, setMsg] = useState<Msg>(null);

  async function load() {
    try {
      const [vs, d] = await Promise.all([api.viewers(), api.dlna()]);
      setViewers(vs);
      setServers(d.status.servers ?? []);
      setDlnaOn(d.settings.enabled);
    } catch (e) {
      setMsg({ ok: false, text: errText(e, "Couldn't load viewers.") });
    }
  }
  useEffect(() => {
    load();
    api.titles().then(setTitles).catch(() => setTitles([]));
  }, []);

  async function remove(v: Viewer) {
    if (!confirm(`Remove ${v.name}? Their watched marks and resume points are deleted too.`)) return;
    try {
      await api.deleteViewer(v.id);
      setMsg({ ok: true, text: `${v.name} removed.` });
      load();
    } catch (e) {
      setMsg({ ok: false, text: errText(e, "Couldn't remove.") });
    }
  }

  function done(changed: boolean) {
    setEditing(null);
    if (changed) {
      setMsg({ ok: true, text: "Saved. TVs may take a minute to see a new or renamed server." });
      load();
    }
  }

  const titleName = (id: string) => titles?.find((t) => t.imdbId === id)?.name ?? id;
  return (
    <section className="card flex flex-col gap-4 p-5">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <h2 className="font-semibold">Viewers</h2>
          <p className="text-sm text-muted">
            Family members who watch on TVs. Each gets their own server in the TV's sources, with their own watched marks and resume points, and you can limit what they see. TVs
            don't sign in, so choose which TVs show each server.
          </p>
        </div>
        {editing === null && <button className="btn-ghost" onClick={() => setEditing("new")}>Add viewer</button>}
      </div>
      {!dlnaOn && (
        <p role="status" className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">
          The DLNA server is off, so viewers' servers don't run. Turn it on in{" "}
          <Link to="/settings?tab=tv" className="underline">TVs (DLNA)</Link>.
        </p>
      )}
      {editing === "new" && <ViewerEditor start={blank} titles={titles} onDone={done} />}
      {viewers?.length === 0 && editing !== "new" && <p className="text-sm text-muted">No viewers yet. Everyone uses the main server and shares one set of resume points.</p>}
      <ul className="flex flex-col gap-3">
        {viewers?.map((v) =>
          editing === v.id ? (
            <li key={v.id}>
              <ViewerEditor start={{ name: v.name, allTitles: v.allTitles, titles: v.titles, dlna: v.dlna, tvs: v.tvs }} id={v.id} titles={titles} onDone={done} />
            </li>
          ) : (
            <li key={v.id} className="flex flex-wrap items-start justify-between gap-3 rounded-xl border border-line p-4">
              <div className="flex min-w-0 flex-col gap-1">
                <span className="font-medium">{v.name}</span>
                <span className="text-sm text-muted">
                  {v.allTitles ? "Whole library" : v.titles.length === 0 ? "No titles chosen yet" : `${v.titles.length} title${v.titles.length > 1 ? "s" : ""}: ${v.titles.slice(0, 3).map(titleName).join(", ")}${v.titles.length > 3 ? "…" : ""}`}
                </span>
                <ServerLine v={v} server={servers.find((s) => s.viewerId === v.id)} />
              </div>
              <div className="flex gap-2">
                <button className="btn-ghost py-1" onClick={() => setEditing(v.id)}>Edit</button>
                <button className="btn-danger py-1" onClick={() => remove(v)}>Remove</button>
              </div>
            </li>
          ),
        )}
      </ul>
      <Note msg={msg} />
    </section>
  );
}

function ServerLine({ v, server }: { v: Viewer; server?: DlnaServer }) {
  if (!v.dlna) return <span className="text-xs text-muted">No server of their own.</span>;
  const where = v.tvs.length === 0 ? "every TV" : `${v.tvs.length} TV${v.tvs.length > 1 ? "s" : ""}`;
  if (!server) return <span className="text-xs text-muted">Server starts with DLNA · on {where}</span>;
  return (
    <span className={`text-xs ${server.running && !server.conflict ? "text-muted" : "text-bad"}`}>
      “{server.name}” on port {server.port} · on {where}
      {server.error ? ` · not running: ${server.error}` : ""}
      {server.conflict ? ` · another device (${server.conflict}) has a server with this name; rename this viewer` : ""}
    </span>
  );
}

// TitleViewers shows, on a title's page, which viewers can see it, and lets
// you add or remove it for viewers limited to chosen titles.
export function TitleViewers({ titleId }: { titleId: string }) {
  const [viewers, setViewers] = useState<Viewer[] | null>(null);
  const [busy, setBusy] = useState<number | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    api.viewers().then(setViewers).catch(() => setViewers([]));
  }, []);

  if (!viewers || viewers.length === 0) return null;

  async function toggle(v: Viewer) {
    const on = !v.titles.includes(titleId);
    setBusy(v.id);
    setError("");
    try {
      const saved = await api.setViewerTitle(v.id, titleId, on);
      setViewers((vs) => vs?.map((x) => (x.id === v.id ? saved : x)) ?? null);
    } catch (e) {
      setError(errText(e, "Couldn't change that."));
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="flex flex-wrap items-center gap-1.5 text-sm">
      <span className="text-xs text-muted">Viewers who can see this:</span>
      {viewers.map((v) => {
        const sees = v.allTitles || v.titles.includes(titleId);
        if (v.allTitles) {
          return (
            <span key={v.id} title={`${v.name} sees the whole library`} className="rounded-full border border-line bg-bg px-2.5 py-0.5 text-xs text-muted">
              {v.name} · all
            </span>
          );
        }
        return (
          <button
            key={v.id}
            type="button"
            aria-pressed={sees}
            disabled={busy === v.id}
            onClick={() => toggle(v)}
            title={sees ? `Hide from ${v.name}` : `Let ${v.name} see this`}
            className={`rounded-full border px-2.5 py-0.5 text-xs ${sees ? "border-deep bg-deep-soft text-deep" : "border-line text-muted hover:text-ink"}`}
          >
            {sees ? "✓ " : ""}
            {v.name}
          </button>
        );
      })}
      <Link to="/settings?tab=viewers" className="text-xs text-deep underline">Manage</Link>
      {error && <span role="alert" className="text-xs text-bad">{error}</span>}
    </div>
  );
}
