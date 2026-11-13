import { useState, type ChangeEvent } from "react";
import { api, type ImportResult } from "../api";

const SECTIONS: { id: string; label: string; hint: string }[] = [
  { id: "settings", label: "Settings", hint: "Library, downloads, TV (DLNA) and update options. This server keeps its own DLNA name, port and network." },
  { id: "suppliers", label: "Suppliers", hint: "Addons, with their order, language and on/off state." },
  { id: "profiles", label: "Quality profiles", hint: "Matched by name; ones with the same name are updated." },
  { id: "mediaServers", label: "Media servers", hint: "Jellyfin, Emby and Plex, with their tokens." },
  { id: "viewers", label: "Viewers", hint: "Matched by name, with the titles they may see." },
  { id: "titles", label: "Tracked titles", hint: "Added in the background, with their profile and the episodes picked." },
];

type Loaded = { name: string; data: { exportedAt?: string; from?: string }; counts: Record<string, number> };

function count(c: { added: number; updated: number; skipped: number }) {
  const parts = [];
  if (c.added) parts.push(`${c.added} added`);
  if (c.updated) parts.push(`${c.updated} updated`);
  if (c.skipped) parts.push(`${c.skipped} already there`);
  return parts.join(", ") || "nothing to change";
}

// Backup exports Reelyard's configuration to a file and imports one.
export function Backup() {
  const [loaded, setLoaded] = useState<Loaded | null>(null);
  const [chosen, setChosen] = useState<string[]>([]);
  const [result, setResult] = useState<ImportResult | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function pick(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    e.target.value = "";
    setError("");
    setResult(null);
    setLoaded(null);
    if (!file) return;
    try {
      const data = JSON.parse(await file.text());
      const dry = await api.importSettings(data, [], true);
      const present = SECTIONS.filter((s) => (dry.sections[s.id] ?? 0) > 0).map((s) => s.id);
      setLoaded({ name: file.name, data, counts: dry.sections });
      setChosen(present);
    } catch (err) {
      setError(err instanceof SyntaxError ? "That file isn't valid JSON." : err instanceof Error ? err.message : "Couldn't read that file.");
    }
  }

  async function run() {
    if (!loaded) return;
    setBusy(true);
    setError("");
    try {
      setResult(await api.importSettings(loaded.data, chosen));
      setLoaded(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "The import failed.");
    } finally {
      setBusy(false);
    }
  }

  const toggle = (id: string) => setChosen((c) => (c.includes(id) ? c.filter((x) => x !== id) : [...c, id]));

  return (
    <>
      <section className="card flex flex-col gap-3 p-5">
        <div>
          <h2 className="font-semibold">Export settings</h2>
          <p className="text-sm text-muted">
            Download Reelyard's configuration as a file: settings, suppliers, quality profiles, media servers, viewers and tracked titles. Use
            it to set up another server or to keep a copy.
          </p>
        </div>
        <p className="rounded-lg bg-accent-soft px-3 py-2 text-sm text-accent">
          Keep the file private: supplier links and media server tokens can contain keys. It never holds passwords, sign-ins, API keys,
          paired devices or addon links.
        </p>
        <div>
          <a className="btn-primary inline-block" href="/api/v1/settings/export" download>
            Download settings file
          </a>
        </div>
      </section>

      <section className="card flex flex-col gap-3 p-5">
        <div>
          <h2 className="font-semibold">Import settings</h2>
          <p className="text-sm text-muted">
            Choose a file exported from Reelyard. Nothing is removed: items are added, and ones that already exist (same name or link) are
            updated.
          </p>
        </div>
        <div>
          <label className="btn-ghost inline-block cursor-pointer">
            Choose file…
            <input type="file" accept="application/json,.json" className="sr-only" onChange={pick} />
          </label>
        </div>

        {loaded && (
          <div className="flex flex-col gap-3 rounded-lg border border-line p-3 text-sm">
            <p>
              <span className="font-medium">{loaded.name}</span>
              <span className="text-muted">
                {loaded.data.exportedAt ? ` · exported ${new Date(loaded.data.exportedAt).toLocaleString()}` : ""}
                {loaded.data.from ? ` · Reelyard ${loaded.data.from}` : ""}
              </span>
            </p>
            <fieldset className="flex flex-col gap-2">
              <legend className="mb-1 text-muted">Import:</legend>
              {SECTIONS.map((s) => {
                const n = loaded.counts[s.id] ?? 0;
                return (
                  <label key={s.id} className={`flex items-start gap-2 ${n ? "" : "opacity-50"}`}>
                    <input type="checkbox" className="mt-0.5" disabled={!n} checked={chosen.includes(s.id)} onChange={() => toggle(s.id)} />
                    <span>
                      {s.label}
                      {s.id !== "settings" && <span className="text-muted"> ({n})</span>}
                      <span className="block text-xs text-muted">{s.hint}</span>
                    </span>
                  </label>
                );
              })}
            </fieldset>
            <div className="flex gap-2">
              <button className="btn-primary" disabled={busy || chosen.length === 0} onClick={run}>
                {busy ? "Importing…" : "Import"}
              </button>
              <button className="btn-ghost" disabled={busy} onClick={() => setLoaded(null)}>
                Cancel
              </button>
            </div>
          </div>
        )}

        {result && (
          <div role="status" className="flex flex-col gap-1 rounded-lg bg-ok-soft px-3 py-2 text-sm">
            <span className="font-medium text-ok">Imported.</span>
            {result.settings.length > 0 && <span>Settings: {result.settings.join(", ")}</span>}
            <span>Suppliers: {count(result.suppliers)}</span>
            <span>Quality profiles: {count(result.profiles)}</span>
            <span>Media servers: {count(result.mediaServers)}</span>
            <span>Viewers: {count(result.viewers)}</span>
            <span>
              Tracked titles: {result.titles.added ? `${result.titles.added} being added in the background` : "none to add"}
              {result.titles.skipped ? `, ${result.titles.skipped} already tracked` : ""}
            </span>
            {result.problems.length > 0 && (
              <ul className="mt-1 list-disc pl-5 text-bad">
                {result.problems.map((p) => (
                  <li key={p}>{p}</li>
                ))}
              </ul>
            )}
          </div>
        )}
        {error && <p role="alert" className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">{error}</p>}
      </section>
    </>
  );
}
