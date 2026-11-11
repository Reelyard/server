import { useEffect, useState, type FormEvent } from "react";
import { api, type Onboarding, type Supplier } from "../api";
import AddonCatalog from "../components/AddonCatalog";
import { Logo } from "../components/Layout";
import { bytes } from "../format";

const STEPS = ["Folders", "Suppliers", "Ready"];

const errText = (e: unknown, fallback: string) => (e instanceof Error ? e.message : fallback);

async function waitForRestart() {
  // The old process answers until it stops; wait for it to go away, then come back.
  const start = Date.now();
  let wentDown = false;
  while (Date.now() - start < 60_000) {
    await new Promise((r) => setTimeout(r, 1000));
    try {
      const res = await fetch("/api/v1/health", { cache: "no-store" });
      if (res.ok && (wentDown || Date.now() - start > 8000)) return true;
    } catch {
      wentDown = true;
    }
  }
  return false;
}

function Folders({ info, onNext, onReload }: { info: Onboarding; onNext: () => void; onReload: () => Promise<void> }) {
  const [dl, setDl] = useState(info.download.path);
  const [lib, setLib] = useState(info.library.path);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const changed = dl.trim() !== info.download.path || lib.trim() !== info.library.path;

  async function save(e: FormEvent) {
    e.preventDefault();
    if (!changed) return onNext();
    setBusy(true);
    setMsg(null);
    try {
      const r = await api.saveFolders(dl, lib);
      if (r.restarting) {
        setMsg({ ok: true, text: "Saved. Harbor is restarting to use the new folders…" });
        if (await waitForRestart()) {
          await onReload();
          onNext();
        } else {
          setMsg({ ok: false, text: "Harbor hasn't come back yet. Reload this page in a moment." });
        }
      } else if (r.changed) {
        setMsg({ ok: true, text: "Saved. Restart Harbor to use the new folders; you can carry on meanwhile." });
      } else {
        onNext();
      }
    } catch (err) {
      setMsg({ ok: false, text: errText(err, "Couldn't save the folders.") });
    } finally {
      setBusy(false);
    }
  }

  const locked = info.locked !== "";
  const saved = msg?.ok && !busy;
  return (
    <form onSubmit={save} className="flex flex-col gap-5">
      <div>
        <h2 className="text-lg font-semibold">Where should files go?</h2>
        <p className="text-sm text-muted">
          Downloads are kept in the download folder while they're shared with others. Finished files are added to the library with
          names Jellyfin, Plex and TVs understand.
        </p>
      </div>
      {[
        { id: "dl", label: "Download folder", value: dl, set: setDl, free: info.download.free },
        { id: "lib", label: "Library folder", value: lib, set: setLib, free: info.library.free },
      ].map((f) => (
        <div key={f.id}>
          <label className="label" htmlFor={`wz-${f.id}`}>{f.label}</label>
          <input id={`wz-${f.id}`} className="input font-mono text-sm" value={f.value} onChange={(e) => f.set(e.target.value)} disabled={locked || busy || saved} spellCheck={false} />
          {f.free >= 0 && <p className="mt-1 text-xs text-muted">{bytes(f.free)} free on this disk now.</p>}
        </div>
      ))}
      {locked ? (
        <p className="rounded-lg bg-bg px-3 py-2 text-sm">{info.locked}</p>
      ) : info.hardlinks ? (
        <p className="rounded-lg bg-ok-soft px-3 py-2 text-sm text-ok">Both folders are on the same disk, so finished files are linked into the library instantly, with no second copy.</p>
      ) : (
        <p className="rounded-lg bg-accent-soft px-3 py-2 text-sm text-accent">
          These folders are on different disks, so finished files are copied into the library and use space twice while they're being shared. Put both on one disk to avoid that.
        </p>
      )}
      {!locked && !info.canRestart && changed && !saved && (
        <p className="text-xs text-muted">New folders are saved to {info.configFile} and used after you restart Harbor.</p>
      )}
      {msg && <p role="status" className={`rounded-lg px-3 py-2 text-sm ${msg.ok ? "bg-ok-soft text-ok" : "bg-bad-soft text-bad"}`}>{msg.text}</p>}
      <div className="flex gap-2">
        {saved ? (
          <button type="button" className="btn-primary" onClick={onNext}>Continue</button>
        ) : (
          <button className="btn-primary" disabled={busy}>{busy ? "Saving…" : changed ? "Save folders" : "Use these folders"}</button>
        )}
      </div>
    </form>
  );
}

function Suppliers({ onNext }: { onNext: () => void }) {
  const [list, setList] = useState<Supplier[]>([]);
  const [url, setUrl] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [browse, setBrowse] = useState(false);
  const load = () => api.suppliers().then(setList).catch(() => {});
  useEffect(() => {
    load();
  }, []);

  async function add(e: FormEvent) {
    e.preventDefault();
    if (!url.trim()) return;
    setBusy(true);
    setError("");
    try {
      await api.addSupplier(url.trim());
      setUrl("");
      load();
    } catch (err) {
      setError(errText(err, "Couldn't add that addon."));
    } finally {
      setBusy(false);
    }
  }

  const hasStreams = list.some((s) => s.resources.includes("stream"));
  const hasSubs = list.some((s) => s.resources.includes("subtitles"));
  return (
    <div className="flex flex-col gap-5">
      <div>
        <h2 className="text-lg font-semibold">Add suppliers</h2>
        <p className="text-sm text-muted">
          Suppliers are Stremio addons. Harbor asks them for torrents of the titles you track, and for subtitles. You need at least one that
          gives torrent streams; one for subtitles is a good idea.
        </p>
      </div>
      <form onSubmit={add} className="flex flex-col gap-2 sm:flex-row">
        <label htmlFor="wz-url" className="sr-only">Addon address</label>
        <input id="wz-url" className="input flex-1" value={url} onChange={(e) => setUrl(e.target.value)} placeholder="Paste an addon's manifest.json address" spellCheck={false} />
        <button className="btn-primary" disabled={busy || !url.trim()}>{busy ? "Adding…" : "Add"}</button>
      </form>
      {error && <p role="alert" className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">{error}</p>}
      {list.length > 0 && (
        <ul className="divide-y divide-line rounded-lg border border-line">
          {list.map((s) => (
            <li key={s.id} className="flex items-center justify-between gap-2 px-3 py-2 text-sm">
              <span className="min-w-0">
                <span className="font-medium">{s.name}</span>
                <span className="block truncate text-xs text-muted">{s.displayUrl}</span>
              </span>
              <span className="shrink-0 text-xs text-muted">{s.resources.filter((r) => r === "stream" || r === "subtitles").join(", ")}</span>
            </li>
          ))}
        </ul>
      )}
      <div>
        <button type="button" className="text-sm text-deep underline" aria-expanded={browse} onClick={() => setBrowse(!browse)}>
          {browse ? "Hide the addon catalog" : "Browse Stremio's addon catalog"}
        </button>
      </div>
      {browse && <AddonCatalog onAdded={load} />}
      <p className="text-sm text-muted">
        {hasStreams
          ? hasSubs
            ? "You're set: Harbor can find torrents and subtitles."
            : "Harbor can find torrents. Add a subtitle addon too if you want subtitles."
          : "No torrent supplier yet. You can add one later in Suppliers."}
      </p>
      <div className="flex gap-2">
        <button className="btn-primary" onClick={onNext}>Continue</button>
      </div>
    </div>
  );
}

function Ready({ onFinish }: { onFinish: (to: string) => void }) {
  return (
    <div className="flex flex-col gap-5">
      <div>
        <h2 className="text-lg font-semibold">Harbor is ready</h2>
        <p className="text-sm text-muted">A few things to know:</p>
      </div>
      <ul className="flex flex-col gap-3 text-sm">
        <li><b>Track a title:</b> search for a movie or series and press <b>Track</b>. Harbor finds a release that fits its quality profile, downloads it, and adds it to the library with subtitles.</li>
        <li><b>Quality profiles</b> set screen size, file size and languages. Three come ready; change them in Quality profiles.</li>
        <li><b>Watch on your TV:</b> Harbor's DLNA server shows up as <b>Harbor</b> in your TV's media menu, or point Jellyfin or Plex at the library folder (Settings).</li>
        <li><b>Privacy:</b> torrent traffic is visible to your internet provider. To use only your VPN, set <code className="font-mono">bind_interface</code> in the config file.</li>
      </ul>
      <p className="text-sm text-muted">Only download content you have the rights to.</p>
      <div className="flex flex-wrap gap-2">
        <button className="btn-primary" onClick={() => onFinish("/search")}>Find something to track</button>
        <button className="btn-ghost" onClick={() => onFinish("/")}>Go to the dashboard</button>
      </div>
    </div>
  );
}

export default function Welcome({ onFinished }: { onFinished: (to: string) => void }) {
  const [info, setInfo] = useState<Onboarding | null>(null);
  const [step, setStep] = useState(0);
  const [error, setError] = useState("");

  const reload = () => api.onboarding().then(setInfo).catch((e) => setError(e.message));
  useEffect(() => {
    reload();
  }, []);

  async function finish(to: string) {
    try {
      await api.finishOnboarding();
    } catch {
      /* not fatal: the wizard just shows again next time */
    }
    onFinished(to);
  }

  return (
    <div className="min-h-full px-4 py-8 sm:py-12">
      <div className="mx-auto flex max-w-2xl flex-col gap-6">
        <header className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center gap-3">
            <Logo className="h-9 w-9" />
            <div>
              <p className="eyebrow">Setup</p>
              <h1 className="text-xl font-semibold">Welcome to Harbor</h1>
            </div>
          </div>
          <button className="text-sm text-muted underline hover:text-ink" onClick={() => finish("/")}>Skip setup</button>
        </header>
        <ol className="flex gap-2 text-sm" aria-label="Setup steps">
          {STEPS.map((s, i) => (
            <li key={s} aria-current={i === step ? "step" : undefined}
              className={`flex flex-1 items-center gap-2 rounded-lg border px-3 py-2 ${i === step ? "border-ink bg-surface font-medium" : i < step ? "border-line text-ok" : "border-line text-muted"}`}>
              <span className={`grid h-5 w-5 shrink-0 place-items-center rounded-full text-xs ${i < step ? "bg-ok-soft" : i === step ? "bg-ink text-bg" : "bg-bg"}`}>{i < step ? "✓" : i + 1}</span>
              {s}
            </li>
          ))}
        </ol>
        <section className="card p-5 sm:p-6">
          {error && <p role="alert" className="text-bad">{error}</p>}
          {!info && !error && <p className="text-muted">Loading…</p>}
          {info && step === 0 && <Folders info={info} onNext={() => setStep(1)} onReload={reload} />}
          {info && step === 1 && <Suppliers onNext={() => setStep(2)} />}
          {info && step === 2 && <Ready onFinish={finish} />}
        </section>
        {step > 0 && (
          <button className="self-start text-sm text-muted underline hover:text-ink" onClick={() => setStep(step - 1)}>Back</button>
        )}
        <p className="text-xs text-muted">You can change all of this later in Settings and Suppliers.</p>
      </div>
    </div>
  );
}
