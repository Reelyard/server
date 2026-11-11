import { useEffect, useState, type FormEvent } from "react";
import { api, type APIKey, type User } from "../api";

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
          <span className="text-ok">New key created. Copy it now: Harbor won't show it again.</span>
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

export default function Settings({ user }: { user: User }) {
  return (
    <div className="flex flex-col gap-6">
      <header>
        <p className="eyebrow">Settings</p>
        <h1 className="mt-1 text-2xl font-semibold tracking-tight">Account</h1>
        <p className="mt-1 text-muted">Signed in as <span className="font-medium text-ink">{user.username}</span>. Download, library and DLNA settings arrive with later milestones.</p>
      </header>
      <Password />
      <ApiKeys />
    </div>
  );
}
