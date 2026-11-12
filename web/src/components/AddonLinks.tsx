import { useEffect, useState } from "react";
import { api, type AddonLink } from "../api";

const local = ["localhost", "127.0.0.1", "[::1]"].includes(window.location.hostname);

// AddonLinks gives each person an install link for Nuvio (or Stremio), so the
// app lists Reelyard's library and plays it from this server.
export function AddonLinks() {
  const [links, setLinks] = useState<AddonLink[] | null>(null);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState<number | null>(null);
  const [busy, setBusy] = useState<number | null>(null);

  function reload() {
    api.addonLinks().then(setLinks).catch((e) => setError(e instanceof Error ? e.message : "Couldn't load the links."));
  }
  useEffect(reload, []);

  async function act(id: number, f: () => Promise<unknown>) {
    setError("");
    setBusy(id);
    try {
      await f();
      setCopied(null);
      reload();
    } catch (e) {
      setError(e instanceof Error ? e.message : "That didn't work.");
    } finally {
      setBusy(null);
    }
  }

  async function copy(l: AddonLink) {
    try {
      await navigator.clipboard.writeText(l.url);
      setCopied(l.userId);
    } catch {
      /* clipboard blocked: the link stays visible to copy by hand */
    }
  }

  return (
    <section className="card flex flex-col gap-4 p-5">
      <div>
        <h2 className="font-semibold">Nuvio and Stremio</h2>
        <p className="text-sm text-muted">
          Add Reelyard as an addon: your library shows up as catalogs, and Reelyard's files appear as sources on titles you have. In Nuvio,
          open Settings → Addons → Add addon and paste the person's link. Viewers only see the titles they're allowed.
        </p>
      </div>
      {local && (
        <p className="rounded-lg bg-accent-soft px-3 py-2 text-sm text-accent">
          You opened Reelyard on this computer, so these links say “{window.location.hostname}” and won't work from a TV or phone. Open
          Reelyard by its network address, then copy the links.
        </p>
      )}
      {links && (
        <ul className="divide-y divide-line rounded-lg border border-line">
          {links.map((l) => (
            <li key={l.userId} className="flex flex-col gap-2 px-3 py-2 text-sm">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <span className="font-medium">
                  {l.name}
                  {!l.viewer && <span className="ml-1.5 font-normal text-muted">(you: the whole library)</span>}
                </span>
                <span className="flex gap-2">
                  {l.url ? (
                    <>
                      <button className="btn-ghost py-1" onClick={() => copy(l)}>{copied === l.userId ? "Copied" : "Copy link"}</button>
                      <button className="btn-ghost py-1" disabled={busy === l.userId} onClick={() => act(l.userId, () => api.newAddonLink(l.userId))} title="The old link stops working">
                        New link
                      </button>
                      <button className="btn-danger py-1" disabled={busy === l.userId} onClick={() => act(l.userId, () => api.deleteAddonLink(l.userId))}>
                        Turn off
                      </button>
                    </>
                  ) : (
                    <button className="btn-primary py-1" disabled={busy === l.userId} onClick={() => act(l.userId, () => api.newAddonLink(l.userId))}>
                      Make link
                    </button>
                  )}
                </span>
              </div>
              {l.url && <code className="rounded bg-bg px-2 py-1 font-mono text-xs break-all select-all">{l.url}</code>}
            </li>
          ))}
        </ul>
      )}
      <p className="text-xs text-muted">
        Anyone with a link can browse and play what it shows, so share it like a password. “New link” or “Turn off” stops the old one.
      </p>
      {error && <p role="alert" className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">{error}</p>}
    </section>
  );
}
