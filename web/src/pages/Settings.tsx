import { useEffect, useState, type FormEvent } from "react";
import { api, type APIKey, type BlockedRelease, type Device, type DownloadSettings, type Language, type NetworkStatus, type User } from "../api";
import { DlnaOptions, MediaServers } from "../components/Playback";

function fmt(d?: string) {
  return d ? new Date(d).toLocaleString() : "never";
}

function Password() {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setMsg(null);
    try {
      await api.changePassword(current, next);
      setCurrent("");
      setNext("");
      setMsg({ ok: true, text: "Password changed. Other devices were signed out." });
    } catch (err) {
      setMsg({ ok: false, text: err instanceof Error ? err.message : "Couldn't change the password." });
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="card flex flex-col gap-4 p-5">
      <h2 className="font-semibold">Password</h2>
      <div className="grid gap-4 sm:grid-cols-2">
        <div>
          <label className="label" htmlFor="pw-current">Current password</label>
          <input id="pw-current" type="password" className="input" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />
        </div>
        <div>
          <label className="label" htmlFor="pw-new">New password</label>
          <input id="pw-new" type="password" className="input" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />
        </div>
      </div>
      {msg && <p role="status" className={`rounded-lg px-3 py-2 text-sm ${msg.ok ? "bg-ok-soft text-ok" : "bg-bad-soft text-bad"}`}>{msg.text}</p>}
      <div><button className="btn-primary" disabled={busy || !current || next.length < 8}>Change password</button></div>
    </form>
  );
}

function ApiKeys() {
  const [keys, setKeys] = useState<APIKey[] | null>(null);
  const [name, setName] = useState("");
  const [secret, setSecret] = useState("");
  const [error, setError] = useState("");
  const [copied, setCopied] = useState(false);

  const reload = () => api.listKeys().then(setKeys).catch((e) => setError(e.message));
  useEffect(() => {
    reload();
  }, []);

  async function create(e: FormEvent) {
    e.preventDefault();
    setError("");
    try {
      const r = await api.createKey(name.trim());
      setSecret(r.secret);
      setCopied(false);
      setName("");
      reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Couldn't create the key.");
    }
  }

  async function remove(id: number) {
    setError("");
    try {
      await api.deleteKey(id);
      reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Couldn't delete the key.");
    }
  }

  async function copy() {
    try {
      await navigator.clipboard.writeText(secret);
      setCopied(true);
    } catch {
      /* clipboard blocked: the key stays visible to copy by hand */
    }
  }

  return (
    <section className="card flex flex-col gap-4 p-5">
      <div>
        <h2 className="font-semibold">API keys</h2>
        <p className="text-sm text-muted">For scripts and other apps. Send the key in the <code className="font-mono">X-Api-Key</code> header.</p>
      </div>
      {secret && (
        <div className="flex flex-col gap-2 rounded-lg bg-ok-soft p-3 text-sm">
          <span className="text-ok">New key created. Copy it now: Reelyard won't show it again.</span>
          <div className="flex flex-wrap items-center gap-2">
            <code className="min-w-0 flex-1 rounded bg-surface px-2 py-1 font-mono break-all select-all">{secret}</code>
            <button type="button" className="btn-ghost py-1" onClick={copy}>{copied ? "Copied" : "Copy"}</button>
          </div>
        </div>
      )}
      {keys && keys.length > 0 && (
        <ul className="divide-y divide-line rounded-lg border border-line">
          {keys.map((k) => (
            <li key={k.id} className="flex flex-wrap items-center justify-between gap-2 px-3 py-2 text-sm">
              <span className="min-w-0">
                <span className="font-medium">{k.name}</span> <code className="font-mono text-muted">{k.prefix}…</code>
                <span className="block text-xs text-muted">Created {fmt(k.createdAt)} · last used {fmt(k.lastUsedAt)}</span>
              </span>
              <button className="btn-danger py-1" onClick={() => remove(k.id)}>Delete</button>
            </li>
          ))}
        </ul>
      )}
      {keys && keys.length === 0 && <p className="text-sm text-muted">No API keys yet.</p>}
      <form onSubmit={create} className="flex flex-wrap items-end gap-2">
        <div className="min-w-0 flex-1">
          <label className="label" htmlFor="key-name">Key name</label>
          <input id="key-name" className="input" placeholder="e.g. home-assistant" value={name} onChange={(e) => setName(e.target.value)} />
        </div>
        <button className="btn-primary" disabled={!name.trim()}>Create key</button>
      </form>
      {error && <p role="alert" className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">{error}</p>}
    </section>
  );
}

const platformNames: Record<string, string> = {
  tizen: "Samsung TV",
  webos: "LG TV",
  androidtv: "Android TV",
  android: "Android",
  ios: "iPhone or iPad",
  macos: "Mac",
  windows: "Windows",
  linux: "Linux",
  web: "Browser",
};

function Devices() {
  const [devices, setDevices] = useState<Device[] | null>(null);
  const [code, setCode] = useState("");
  const [error, setError] = useState("");
  const [approved, setApproved] = useState("");

  const reload = () => api.devices().then(setDevices).catch((e) => setError(e.message));
  useEffect(() => {
    reload();
    // A device waiting for its code to be entered appears without a reload.
    const t = window.setInterval(reload, 5000);
    return () => window.clearInterval(t);
  }, []);

  async function approve(e: FormEvent) {
    e.preventDefault();
    setError("");
    setApproved("");
    try {
      const d = await api.approveDevice(code);
      setApproved(d.name);
      setCode("");
      reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Couldn't approve the device.");
    }
  }

  async function remove(d: Device) {
    setError("");
    try {
      await api.removeDevice(d.id);
      reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Couldn't remove the device.");
    }
  }

  const status = (d: Device) =>
    d.paired ? `Signed in · last used ${fmt(d.lastUsedAt)}` : d.approvedAt ? "Approved, finishing sign-in…" : "Waiting for its code";

  return (
    <section className="card flex flex-col gap-4 p-5">
      <div>
        <h2 className="font-semibold">Devices</h2>
        <p className="text-sm text-muted">
          TVs, phones and computers running the Reelyard app. To add one, open the app on it and enter the code it shows here.
        </p>
      </div>
      <form onSubmit={approve} className="flex flex-wrap items-end gap-2">
        <div className="min-w-0 flex-1">
          <label className="label" htmlFor="device-code">Code shown on the device</label>
          <input
            id="device-code"
            className="input font-mono uppercase tracking-widest"
            placeholder="K7Q-4MD"
            autoComplete="off"
            spellCheck={false}
            maxLength={9}
            value={code}
            onChange={(e) => setCode(e.target.value)}
          />
        </div>
        <button className="btn-primary" disabled={code.replace(/[^a-z0-9]/gi, "").length !== 6}>Approve</button>
      </form>
      {approved && <p className="rounded-lg bg-ok-soft px-3 py-2 text-sm text-ok">Approved {approved}. It signs in within a few seconds.</p>}
      {devices && devices.length > 0 && (
        <ul className="divide-y divide-line rounded-lg border border-line">
          {devices.map((d) => (
            <li key={d.id} className="flex flex-wrap items-center justify-between gap-2 px-3 py-2 text-sm">
              <span className="min-w-0">
                <span className="font-medium">{d.name}</span>
                {d.platform && <span className="text-muted"> · {platformNames[d.platform] ?? d.platform}</span>}
                <span className="block text-xs text-muted">{status(d)}</span>
              </span>
              <button className="btn-danger py-1" onClick={() => remove(d)}>{d.paired ? "Sign out" : d.approvedAt ? "Remove" : "Turn down"}</button>
            </li>
          ))}
        </ul>
      )}
      {devices && devices.length === 0 && <p className="text-sm text-muted">No devices yet.</p>}
      {error && <p role="alert" className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">{error}</p>}
    </section>
  );
}

function DownloadOptions() {
  const [s, setS] = useState<DownloadSettings | null>(null);
  const [port, setPort] = useState(0);
  const [net, setNet] = useState<NetworkStatus | null>(null);
  const [free, setFree] = useState(-1);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.downloadSettings().then((r) => (setS(r.settings), setPort(r.port), setNet(r.network), setFree(r.free))).catch((e) => setMsg({ ok: false, text: e.message }));
  }, []);

  async function save(e: FormEvent) {
    e.preventDefault();
    if (!s) return;
    setBusy(true);
    setMsg(null);
    try {
      const r = await api.saveDownloadSettings(s);
      setS(r.settings);
      setMsg({ ok: true, text: "Saved. Changes apply right away." });
    } catch (err) {
      setMsg({ ok: false, text: err instanceof Error ? err.message : "Couldn't save." });
    } finally {
      setBusy(false);
    }
  }

  const num = (k: keyof DownloadSettings) => (e: React.ChangeEvent<HTMLInputElement>) =>
    s && setS({ ...s, [k]: Math.max(0, Number(e.target.value) || 0) });

  return (
    <form onSubmit={save} className="card flex flex-col gap-4 p-5">
      <div>
        <h2 className="font-semibold">Downloads</h2>
        <p className="text-sm text-muted">
          Peers connect on port <span className="font-mono">{port || "…"}</span> (TCP and UDP). Forwarding it in your router helps speeds; change it with <code className="font-mono">torrent_port</code> in the config file.
        </p>
      </div>
      {s && (
        <div className="grid gap-4 sm:grid-cols-3">
          <div>
            <label className="label" htmlFor="max-active">Downloads at once</label>
            <input id="max-active" type="number" min={0} max={50} className="input" value={s.maxActive} onChange={num("maxActive")} />
            <p className="mt-1 text-xs text-muted">0 = no limit. Finished files sharing don't count.</p>
          </div>
          <div>
            <label className="label" htmlFor="dl-limit">Download limit (KB/s)</label>
            <input id="dl-limit" type="number" min={0} className="input" value={s.downloadLimitKBps} onChange={num("downloadLimitKBps")} />
            <p className="mt-1 text-xs text-muted">0 = unlimited.</p>
          </div>
          <div>
            <label className="label" htmlFor="ul-limit">Upload limit (KB/s)</label>
            <input id="ul-limit" type="number" min={0} className="input" value={s.uploadLimitKBps} onChange={num("uploadLimitKBps")} />
            <p className="mt-1 text-xs text-muted">0 = unlimited.</p>
          </div>
          <div>
            <label className="label" htmlFor="seed-ratio">Stop sharing at ratio</label>
            <input id="seed-ratio" type="number" min={0} step={0.1} className="input" value={s.seedRatio}
              onChange={(e) => setS({ ...s, seedRatio: Math.max(0, Number(e.target.value) || 0) })} />
            <p className="mt-1 text-xs text-muted">Uploaded ÷ size. 0 = no ratio limit.</p>
          </div>
          <div>
            <label className="label" htmlFor="seed-hours">…or after hours</label>
            <input id="seed-hours" type="number" min={0} className="input" value={s.seedHours} onChange={num("seedHours")} />
            <p className="mt-1 text-xs text-muted">Whichever comes first. Tracked titles use their profile's limits.</p>
          </div>
          <div>
            <label className="label" htmlFor="stall">Give up on stalled downloads after (minutes)</label>
            <input id="stall" type="number" min={0} className="input" value={s.stallMinutes} onChange={num("stallMinutes")} />
            <p className="mt-1 text-xs text-muted">No progress this long fails it; tracked titles try another release. 0 = never.</p>
          </div>
          <div>
            <label className="label" htmlFor="min-free">Keep free on disk (GB)</label>
            <input id="min-free" type="number" min={0} className="input" value={s.minFreeGB} onChange={num("minFreeGB")} />
            <p className="mt-1 text-xs text-muted">{free >= 0 ? `${(free / 2 ** 30).toFixed(1)} GB free now in the download folder.` : "Downloads that wouldn't fit are refused."}</p>
          </div>
        </div>
      )}
      <div className="rounded-lg bg-bg px-3 py-2 text-sm">
        {!net?.interface ? (
          <>Torrents use any network connection. To send them only through your VPN, set <code className="font-mono">bind_interface</code> (e.g. <code className="font-mono">wg0</code>) in the config file and restart Reelyard.</>
        ) : net.ok ? (
          <span className="text-ok">Torrents only use <b>{net.interface}</b> ({net.address}). If it goes down, they pause.</span>
        ) : (
          <span className="text-bad">{net.message}</span>
        )}
      </div>
      {msg && <p role="status" className={`rounded-lg px-3 py-2 text-sm ${msg.ok ? "bg-ok-soft text-ok" : "bg-bad-soft text-bad"}`}>{msg.text}</p>}
      <div><button className="btn-primary" disabled={busy || !s}>Save download settings</button></div>
    </form>
  );
}

function LibraryOptions() {
  const [langs, setLangs] = useState<string[] | null>(null);
  const [all, setAll] = useState<Language[]>([]);
  const [dir, setDir] = useState("");
  const [pick, setPick] = useState("");
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.librarySettings().then((r) => (setLangs(r.settings.subtitleLangs), setDir(r.libraryDir))).catch((e) => setMsg({ ok: false, text: e.message }));
    api.languages().then(setAll).catch(() => {});
  }, []);

  const name = (c: string) => all.find((l) => l.code === c)?.name ?? c;

  async function save(next: string[]) {
    setBusy(true);
    setMsg(null);
    try {
      const r = await api.saveLibrarySettings({ subtitleLangs: next });
      setLangs(r.settings.subtitleLangs);
      setMsg({ ok: true, text: "Saved. Applies to the next imports and to “Find subtitles”." });
    } catch (err) {
      setMsg({ ok: false, text: err instanceof Error ? err.message : "Couldn't save." });
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="card flex flex-col gap-4 p-5">
      <div>
        <h2 className="font-semibold">Library</h2>
        <p className="text-sm text-muted">
          Finished downloads are linked into <span className="font-mono break-all">{dir || "…"}</span> with Jellyfin/Plex names. Change the folder with <code className="font-mono">library_dir</code> in the config file.
        </p>
      </div>
      <div className="flex flex-col gap-2">
        <span className="label">Subtitle languages</span>
        <p className="text-xs text-muted">Reelyard saves a subtitle file for each of these unless the video already has one built in. Uses your subtitle suppliers (e.g. OpenSubtitles).</p>
        <div className="flex flex-wrap items-center gap-1.5">
          {langs?.length === 0 && <span className="text-sm text-muted">None: subtitles are not downloaded.</span>}
          {langs?.map((l, i) => (
            <span key={l} className="flex items-center gap-1 rounded-full bg-deep-soft py-0.5 pr-1 pl-2.5 text-sm text-deep">
              {name(l)}
              <button
                type="button"
                className="grid h-5 w-5 place-items-center rounded-full hover:bg-surface"
                aria-label={`Remove ${name(l)}`}
                disabled={busy}
                onClick={() => save(langs.filter((_, j) => j !== i))}
              >
                ×
              </button>
            </span>
          ))}
        </div>
        <div className="flex flex-wrap gap-2">
          <label htmlFor="add-lang" className="sr-only">Add a language</label>
          <select id="add-lang" className="input w-auto" value={pick} onChange={(e) => setPick(e.target.value)}>
            <option value="">Add a language…</option>
            {all.filter((l) => !langs?.includes(l.code)).map((l) => (
              <option key={l.code} value={l.code}>{l.name}</option>
            ))}
          </select>
          <button type="button" className="btn-ghost" disabled={!pick || busy || !langs} onClick={() => (save([...(langs ?? []), pick]), setPick(""))}>Add</button>
        </div>
      </div>
      {msg && <p role="status" className={`rounded-lg px-3 py-2 text-sm ${msg.ok ? "bg-ok-soft text-ok" : "bg-bad-soft text-bad"}`}>{msg.text}</p>}
    </section>
  );
}

function Blocklist() {
  const [list, setList] = useState<BlockedRelease[] | null>(null);
  const [error, setError] = useState("");
  const load = () => api.blocklist().then(setList).catch((e) => setError(e.message));
  useEffect(() => {
    load();
  }, []);

  async function unblock(b: BlockedRelease) {
    setError("");
    try {
      await api.unblock(b);
      load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't unblock.");
    }
  }

  return (
    <section className="card flex flex-col gap-3 p-5">
      <div>
        <h2 className="font-semibold">Blocked releases</h2>
        <p className="text-sm text-muted">Releases Reelyard won't grab again for a title: they failed, stalled, were removed, or had the wrong audio language.</p>
      </div>
      {list && list.length === 0 && <p className="text-sm text-muted">Nothing blocked.</p>}
      {list && list.length > 0 && (
        <ul className="divide-y divide-line rounded-lg border border-line">
          {list.map((b) => (
            <li key={b.infoHash + b.fileIdx + b.itemId} className="flex flex-wrap items-center justify-between gap-2 px-3 py-2 text-sm">
              <span className="min-w-0 flex-1">
                <span className="block font-mono text-xs break-all">{b.name || b.infoHash}</span>
                <span className="text-xs text-muted">{b.itemId} · {b.reason} · {new Date(b.createdAt).toLocaleDateString()}</span>
              </span>
              <button className="btn-ghost py-1" onClick={() => unblock(b)}>Unblock</button>
            </li>
          ))}
        </ul>
      )}
      {error && <p role="alert" className="text-sm text-bad">{error}</p>}
    </section>
  );
}

export default function Settings({ user }: { user: User }) {
  return (
    <div className="flex flex-col gap-6">
      <header>
        <p className="eyebrow">Settings</p>
        <h1 className="mt-1 text-2xl font-semibold tracking-tight">Settings</h1>
        <p className="mt-1 text-muted">Signed in as <span className="font-medium text-ink">{user.username}</span>.</p>
      </header>
      <LibraryOptions />
      <DlnaOptions />
      <MediaServers />
      <DownloadOptions />
      <Blocklist />
      <Devices />
      <Password />
      <ApiKeys />
    </div>
  );
}
