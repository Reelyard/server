import { useEffect, useState, type FormEvent } from "react";
import { api, type DlnaState, type MediaServer, type MediaServerInput, type MediaServerKind } from "../api";
import { ago } from "../format";

type Msg = { ok: boolean; text: string } | null;

function Note({ msg }: { msg: Msg }) {
  if (!msg) return null;
  return <p role="status" className={`rounded-lg px-3 py-2 text-sm ${msg.ok ? "bg-ok-soft text-ok" : "bg-bad-soft text-bad"}`}>{msg.text}</p>;
}

const errText = (e: unknown, fallback: string) => (e instanceof Error ? e.message : fallback);

export function DlnaOptions() {
  const [st, setSt] = useState<DlnaState | null>(null);
  const [msg, setMsg] = useState<Msg>(null);
  const [busy, setBusy] = useState(false);
  const [showVirtual, setShowVirtual] = useState(false);

  useEffect(() => {
    api.dlna().then(setSt).catch((e) => setMsg({ ok: false, text: e.message }));
  }, []);

  const s = st?.settings;
  const set = (patch: Partial<DlnaState["settings"]>) => st && setSt({ ...st, settings: { ...st.settings, ...patch } });

  async function save(e: FormEvent) {
    e.preventDefault();
    if (!s) return;
    setBusy(true);
    setMsg(null);
    try {
      const r = await api.saveDlna(s);
      setSt(r);
      setMsg(
        r.status.error
          ? { ok: false, text: `Saved, but the server couldn't start: ${r.status.error}` }
          : { ok: true, text: r.settings.enabled ? "Saved. The server restarted; TVs may take a minute to see the change." : "Saved. The DLNA server is off." },
      );
    } catch (err) {
      setMsg({ ok: false, text: errText(err, "Couldn't save.") });
    } finally {
      setBusy(false);
    }
  }

  function toggleIfc(name: string) {
    if (!s) return;
    set({ interfaces: s.interfaces.includes(name) ? s.interfaces.filter((x) => x !== name) : [...s.interfaces, name] });
  }

  const status = st?.status;
  return (
    <form onSubmit={save} className="card flex flex-col gap-4 p-5">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <h2 className="font-semibold">DLNA server</h2>
          <p className="text-sm text-muted">Smart TVs, game consoles and players like VLC on your network can browse and play the library directly. Only devices on local networks can connect.</p>
        </div>
        {status && (
          <span className={`rounded px-2 py-0.5 text-xs ${status.running ? "bg-ok-soft text-ok" : status.error ? "bg-bad-soft text-bad" : "bg-bg text-muted"}`}>
            {status.running ? "Running" : status.error ? "Not running" : "Off"}
          </span>
        )}
      </div>
      {s && (
        <>
          <label className="flex items-center gap-2 text-sm">
            <input id="dlna-on" type="checkbox" checked={s.enabled} onChange={(e) => set({ enabled: e.target.checked })} className="accent-[var(--hb-deep)]" />
            Share the library over DLNA
          </label>
          <div className="grid gap-4 sm:grid-cols-2">
            <div>
              <label className="label" htmlFor="dlna-name">Name shown on TVs</label>
              <input id="dlna-name" className="input" maxLength={64} value={s.name} onChange={(e) => set({ name: e.target.value })} />
            </div>
            <div>
              <label className="label" htmlFor="dlna-port">Port</label>
              <input id="dlna-port" type="number" min={1024} max={65535} className="input" value={s.port} onChange={(e) => set({ port: Number(e.target.value) || 0 })} />
            </div>
          </div>
          <fieldset className="flex flex-col gap-2">
            <legend className="label">Networks</legend>
            <p className="text-xs text-muted">
              {s.interfaces.length === 0 ? "Announced on every local network except container and VM bridges. Tick some to choose." : "Announced only on the ticked networks."} Your VPN interface is never used.
            </p>
            <div className="flex flex-wrap gap-3">
              {st.interfaces.length === 0 && <span className="text-sm text-muted">No network found.</span>}
              {st.interfaces.filter((i) => !i.virtual || showVirtual || s.interfaces.includes(i.name)).map((i) => (
                <label key={i.name} className="flex items-center gap-1.5 text-sm">
                  <input id={`dlna-if-${i.name}`} type="checkbox" checked={s.interfaces.includes(i.name)} onChange={() => toggleIfc(i.name)} className="accent-[var(--hb-deep)]" />
                  <span className="font-mono">{i.name}</span>
                  <span className="text-muted">{i.addrs.join(", ")}</span>
                </label>
              ))}
              {s.interfaces
                .filter((n) => !st.interfaces.some((i) => i.name === n))
                .map((n) => (
                  <label key={n} className="flex items-center gap-1.5 text-sm text-muted">
                    <input type="checkbox" checked onChange={() => toggleIfc(n)} className="accent-[var(--hb-deep)]" />
                    <span className="font-mono">{n}</span> (not connected)
                  </label>
                ))}
            </div>
            {st.interfaces.some((i) => i.virtual && !s.interfaces.includes(i.name)) && (
              <button type="button" className="self-start text-xs text-deep underline" onClick={() => setShowVirtual(!showVirtual)}>
                {showVirtual ? "Hide" : "Show"} container and VM networks ({st.interfaces.filter((i) => i.virtual).length})
              </button>
            )}
          </fieldset>
          <label className="flex items-start gap-2 text-sm">
            <input id="dlna-transcode" type="checkbox" className="mt-1 accent-[var(--hb-deep)]" checked={s.transcode} disabled={!status?.ffmpeg && !s.transcode} onChange={(e) => set({ transcode: e.target.checked })} />
            <span>
              Offer converted versions for TVs that can't play a file
              <span className="block text-xs text-muted">
                {status?.ffmpeg ? "Uses ffmpeg and a lot of CPU while playing." : "Needs ffmpeg, which wasn't found on this computer."}
              </span>
            </span>
          </label>
          {status?.running && status.addresses.length > 0 && (
            <p className="rounded-lg bg-bg px-3 py-2 text-sm">
              Look for <b>{s.name}</b> in your TV's media or sources menu. Reachable at{" "}
              {status.addresses.map((a, i) => (
                <span key={a}>
                  {i > 0 && ", "}
                  <span className="font-mono">{a}</span>
                </span>
              ))}
              . Subtitles saved next to a video are offered with it.
              {!status.ffprobe && " Install ffprobe so TVs can show durations."}
            </p>
          )}
        </>
      )}
      <Note msg={msg} />
      {!msg && status?.error && <Note msg={{ ok: false, text: status.error }} />}
      <div><button className="btn-primary" disabled={busy || !s}>Save DLNA settings</button></div>
    </form>
  );
}

const KINDS: { v: MediaServerKind; label: string; port: number; tokenHelp: string }[] = [
  { v: "jellyfin", label: "Jellyfin", port: 8096, tokenHelp: "Dashboard → API Keys → +." },
  { v: "emby", label: "Emby", port: 8096, tokenHelp: "Settings → Advanced → API Keys → New API key." },
  { v: "plex", label: "Plex", port: 32400, tokenHelp: "Your X-Plex-Token: open any item's “Get Info” → “View XML” in Plex Web and copy it from the address." },
];

function ServerEditor({ start, onDone }: { start: MediaServerInput; onDone: () => void }) {
  const [m, setM] = useState<MediaServerInput>(start);
  const [msg, setMsg] = useState<Msg>(null);
  const [busy, setBusy] = useState(false);
  const kind = KINDS.find((k) => k.v === m.kind)!;
  const set = (patch: Partial<MediaServerInput>) => setM((x) => ({ ...x, ...patch }));

  async function test() {
    setBusy(true);
    setMsg(null);
    try {
      const r = await api.testMediaServer(m);
      setMsg(r.ok ? { ok: true, text: `Connected to ${kind.label}.` } : { ok: false, text: r.error ?? "Couldn't connect." });
    } catch (e) {
      setMsg({ ok: false, text: errText(e, "Couldn't connect.") });
    } finally {
      setBusy(false);
    }
  }

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setMsg(null);
    try {
      await api.saveMediaServer(m);
      onDone();
    } catch (err) {
      setMsg({ ok: false, text: errText(err, "Couldn't save.") });
      setBusy(false);
    }
  }

  return (
    <form onSubmit={save} className="flex flex-col gap-3 rounded-lg border border-line p-4">
      <fieldset className="flex flex-wrap gap-1.5">
        <legend className="sr-only">Type</legend>
        {KINDS.map((k) => (
          <button
            type="button"
            key={k.v}
            aria-pressed={m.kind === k.v}
            onClick={() => set({ kind: k.v })}
            className={`rounded-full border px-3 py-1 text-sm ${m.kind === k.v ? "border-ink bg-ink text-bg" : "border-line bg-surface hover:bg-bg"}`}
          >
            {k.label}
          </button>
        ))}
      </fieldset>
      <div className="grid gap-3 sm:grid-cols-2">
        <div>
          <label className="label" htmlFor="ms-url">Address</label>
          <input id="ms-url" className="input" value={m.url} onChange={(e) => set({ url: e.target.value })} placeholder={`http://192.168.1.10:${kind.port}`} required />
        </div>
        <div>
          <label className="label" htmlFor="ms-name">Name (optional)</label>
          <input id="ms-name" className="input" value={m.name} onChange={(e) => set({ name: e.target.value })} placeholder={kind.label} />
        </div>
      </div>
      <div>
        <label className="label" htmlFor="ms-token">{m.kind === "plex" ? "Plex token" : "API key"}</label>
        <input
          id="ms-token"
          type="password"
          autoComplete="off"
          className="input"
          value={m.token}
          onChange={(e) => set({ token: e.target.value })}
          placeholder={m.id ? "Saved. Type a new one to replace it." : ""}
        />
        <p className="mt-1 text-xs text-muted">{kind.tokenHelp}</p>
      </div>
      <label className="flex items-center gap-2 text-sm">
        <input id="ms-enabled" type="checkbox" checked={m.enabled} onChange={(e) => set({ enabled: e.target.checked })} className="accent-[var(--hb-deep)]" />
        Refresh after new files are added
      </label>
      <Note msg={msg} />
      <div className="flex flex-wrap gap-2">
        <button className="btn-primary" disabled={busy}>{m.id ? "Save" : "Add server"}</button>
        <button type="button" className="btn-ghost" disabled={busy || !m.url} onClick={test}>Test connection</button>
        <button type="button" className="btn-ghost" onClick={onDone}>Cancel</button>
      </div>
    </form>
  );
}

export function MediaServers() {
  const [list, setList] = useState<MediaServer[] | null>(null);
  const [editing, setEditing] = useState<MediaServerInput | null>(null);
  const [msg, setMsg] = useState<Msg>(null);
  const [busy, setBusy] = useState(false);
  const load = () => api.mediaServers().then(setList).catch((e) => setMsg({ ok: false, text: e.message }));
  useEffect(() => {
    load();
  }, []);

  async function remove(m: MediaServer) {
    setMsg(null);
    try {
      await api.deleteMediaServer(m.id);
      load();
    } catch (e) {
      setMsg({ ok: false, text: errText(e, "Couldn't remove.") });
    }
  }

  async function refresh() {
    setBusy(true);
    setMsg(null);
    try {
      const r = await api.refreshMediaServers();
      setList(r);
      const bad = r.filter((m) => m.enabled && m.lastError);
      setMsg(bad.length ? { ok: false, text: `${bad.map((m) => m.name).join(", ")} didn't refresh.` } : { ok: true, text: "Asked them to rescan." });
    } catch (e) {
      setMsg({ ok: false, text: errText(e, "Couldn't refresh.") });
    } finally {
      setBusy(false);
    }
  }

  const label = (k: MediaServerKind) => KINDS.find((x) => x.v === k)?.label ?? k;

  return (
    <section className="card flex flex-col gap-4 p-5">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <h2 className="font-semibold">Jellyfin, Emby and Plex</h2>
          <p className="text-sm text-muted">Point your media server at the library folder, then add it here so Harbor asks it to rescan when new files arrive.</p>
        </div>
        {list && list.some((m) => m.enabled) && !editing && (
          <button className="btn-ghost" disabled={busy} onClick={refresh}>Refresh now</button>
        )}
      </div>
      {list && list.length === 0 && !editing && <p className="text-sm text-muted">No media server added.</p>}
      {list && list.length > 0 && (
        <ul className="divide-y divide-line rounded-lg border border-line">
          {list.map((m) => (
            <li key={m.id} className="flex flex-wrap items-center justify-between gap-2 px-3 py-2 text-sm">
              <span className="min-w-0 flex-1">
                <span className="font-medium">{m.name}</span> <span className="text-muted">· {label(m.kind)}{m.enabled ? "" : " · paused"}</span>
                <span className="block font-mono text-xs break-all text-muted">{m.url}</span>
                {m.lastAt && (
                  <span className={`block text-xs ${m.lastError ? "text-bad" : "text-ok"}`}>
                    {m.lastError ? `Refresh failed ${ago(m.lastAt)}: ${m.lastError}` : `Refreshed ${ago(m.lastAt)}`}
                  </span>
                )}
              </span>
              <span className="flex gap-1.5">
                <button className="btn-ghost py-1" onClick={() => setEditing({ id: m.id, kind: m.kind, name: m.name, url: m.url, token: "", enabled: m.enabled })}>Edit</button>
                <button className="btn-ghost py-1 text-bad" onClick={() => remove(m)}>Remove</button>
              </span>
            </li>
          ))}
        </ul>
      )}
      {editing ? (
        <ServerEditor key={editing.id ?? "new"} start={editing} onDone={() => (setEditing(null), load())} />
      ) : (
        <div><button className="btn-ghost" onClick={() => setEditing({ kind: "jellyfin", name: "", url: "", token: "", enabled: true })}>Add media server</button></div>
      )}
      <Note msg={msg} />
    </section>
  );
}
