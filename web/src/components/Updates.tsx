import { useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import { api, type UpdateStatus } from "../api";

const DISMISS_KEY = "reelyard:dismissed-update";
const RECHECK_MS = 30 * 60 * 1000;

const methodNames: Record<UpdateStatus["method"], string> = {
  binary: "Program file",
  docker: "Docker",
  package: "Package (.deb / .rpm)",
  homebrew: "Homebrew",
  dev: "Development build",
};

function plainVersion(v: string) {
  return v.replace(/^v/, "");
}

// waitForRestart polls until Reelyard answers again with another version,
// then reloads the page onto it.
async function waitForRestart(from: string) {
  const deadline = Date.now() + 3 * 60 * 1000;
  while (Date.now() < deadline) {
    await new Promise((r) => setTimeout(r, 2000));
    try {
      const res = await fetch("/api/v1/health", { cache: "no-store" });
      if (res.ok) {
        const h = (await res.json()) as { version?: string };
        if (h.version && h.version !== from) {
          window.location.reload();
          return;
        }
      }
    } catch {
      /* still restarting */
    }
  }
}

// useUpdates loads the update status, and follows an update in progress.
function useUpdates(recheck = 0) {
  const [status, setStatus] = useState<UpdateStatus | null>(null);
  const waiting = useRef(false);

  function follow(s: UpdateStatus) {
    setStatus(s);
    if (s.state === "restarting" && !waiting.current) {
      waiting.current = true;
      void waitForRestart(s.current);
    }
  }

  useEffect(() => {
    let live = true;
    const load = () =>
      api
        .updates()
        .then((s) => live && follow(s))
        .catch(() => {
          /* not signed in yet, or the server is restarting */
        });
    load();
    const t = recheck ? setInterval(load, recheck) : undefined;
    return () => {
      live = false;
      if (t) clearInterval(t);
    };
  }, [recheck]); // eslint-disable-line react-hooks/exhaustive-deps

  // While installing, ask every two seconds how it's going.
  useEffect(() => {
    if (status?.state !== "updating") return;
    const t = setInterval(() => api.updates().then(follow).catch(() => {}), 2000);
    return () => clearInterval(t);
  }, [status?.state]); // eslint-disable-line react-hooks/exhaustive-deps

  return [status, follow] as const;
}

function readDismissed() {
  try {
    return localStorage.getItem(DISMISS_KEY) ?? "";
  } catch {
    return "";
  }
}

// UpdateBanner sits above every page when a newer version is out.
export function UpdateBanner() {
  const [status, follow] = useUpdates(RECHECK_MS);
  const [dismissed, setDismissed] = useState(readDismissed);
  const [error, setError] = useState("");

  if (!status) return null;
  const busy = status.state === "updating" || status.state === "restarting";
  if (!busy && (!status.available || !status.latest || dismissed === status.latest.version)) return null;

  async function apply() {
    setError("");
    try {
      follow(await api.applyUpdate());
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't start the update.");
    }
  }

  function later() {
    if (!status?.latest) return;
    try {
      localStorage.setItem(DISMISS_KEY, status.latest.version);
    } catch {
      /* storage blocked: hide it for this visit only */
    }
    setDismissed(status.latest.version);
  }

  const latest = status.latest;
  return (
    <div role="status" className="mb-6 flex flex-wrap items-center justify-between gap-3 rounded-lg border border-deep/30 bg-deep-soft px-4 py-3 text-sm">
      <span className="min-w-0 text-ink">
        {status.state === "updating" && <>Downloading and installing Reelyard {latest && plainVersion(latest.version)}…</>}
        {status.state === "restarting" && <>Restarting Reelyard on the new version. This page reloads when it's back.</>}
        {!busy && latest && (
          <>
            <span className="font-medium">Reelyard {plainVersion(latest.version)} is available</span>{" "}
            <span className="text-muted">(you have {plainVersion(status.current)}).</span>
            {status.state === "failed" && <span className="block text-bad">The last attempt failed: {status.stateError}</span>}
          </>
        )}
        {error && <span className="block text-bad">{error}</span>}
      </span>
      {!busy && latest && (
        <span className="flex flex-wrap items-center gap-2">
          <a className="btn-ghost py-1" href={latest.url} target="_blank" rel="noreferrer">What's new</a>
          {status.canApply ? (
            <button className="btn-primary py-1" onClick={apply}>Update now</button>
          ) : (
            <Link className="btn-primary py-1" to="/settings?tab=updates">How to update</Link>
          )}
          <button className="btn-ghost py-1" onClick={later}>Later</button>
        </span>
      )}
    </div>
  );
}

// UpdateSettings is the Settings → Updates tab.
export function UpdateSettings() {
  const [status, follow] = useUpdates();
  const [error, setError] = useState("");
  const [checking, setChecking] = useState(false);

  async function run(f: () => Promise<UpdateStatus>) {
    setError("");
    try {
      follow(await f());
    } catch (e) {
      setError(e instanceof Error ? e.message : "That didn't work.");
    }
  }

  if (!status) return <section className="card p-5 text-sm text-muted">Loading…</section>;
  const latest = status.latest;
  const busy = status.state === "updating" || status.state === "restarting";

  return (
    <>
      <section className="card flex flex-col gap-4 p-5">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h2 className="font-semibold">Version</h2>
            <p className="text-sm text-muted">
              You're running <span className="font-mono text-ink">{status.current}</span> · installed as {methodNames[status.method]}
            </p>
          </div>
          <button
            className="btn-ghost"
            disabled={checking || busy}
            onClick={async () => {
              setChecking(true);
              await run(api.checkUpdates);
              setChecking(false);
            }}
          >
            {checking ? "Checking…" : "Check now"}
          </button>
        </div>

        {status.checkError && <p className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">{status.checkError}</p>}
        {!status.checkError && status.checkedAt && !latest && (
          <p className="text-sm text-muted">No published releases on this channel yet.</p>
        )}
        {!status.checkedAt && !status.checkError && <p className="text-sm text-muted">Reelyard checks for new versions a minute after it starts, then every 6 hours.</p>}

        {latest && (
          <div className={`flex flex-col gap-2 rounded-lg p-3 text-sm ${status.available ? "bg-deep-soft" : "bg-bg"}`}>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span>
                {status.available ? (
                  <span className="font-medium">Reelyard {plainVersion(latest.version)} is available</span>
                ) : (
                  <span>You're up to date. Latest: {plainVersion(latest.version)}</span>
                )}
                <span className="text-muted">
                  {" "}
                  · {new Date(latest.publishedAt).toLocaleDateString()}
                  {latest.prerelease ? " · beta" : ""}
                </span>
              </span>
              <span className="flex gap-2">
                <a className="btn-ghost py-1" href={latest.url} target="_blank" rel="noreferrer">Release page</a>
                {status.available && status.canApply && (
                  <button className="btn-primary py-1" disabled={busy} onClick={() => run(api.applyUpdate)}>
                    {status.state === "updating" ? "Installing…" : status.state === "restarting" ? "Restarting…" : "Update now"}
                  </button>
                )}
              </span>
            </div>
            {status.available && latest.notes && (
              <pre className="max-h-64 overflow-auto whitespace-pre-wrap rounded bg-surface p-2 font-sans text-xs text-muted">{latest.notes}</pre>
            )}
          </div>
        )}
        {status.state === "failed" && <p className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">The update failed: {status.stateError}</p>}
        {status.state === "restarting" && <p className="text-sm text-muted">Restarting on the new version. This page reloads when Reelyard is back.</p>}

        {!status.canApply && (
          <div className="rounded-lg border border-line p-3 text-sm">
            <p>{status.reason}</p>
            {status.instructions && <p className="mt-1 text-muted">{status.instructions}</p>}
          </div>
        )}
      </section>

      <section className="card flex flex-col gap-4 p-5">
        <h2 className="font-semibold">Update options</h2>
        <div className="max-w-xs">
          <label className="label" htmlFor="update-channel">Channel</label>
          <select
            id="update-channel"
            className="input"
            value={status.settings.channel}
            onChange={(e) => run(() => api.saveUpdateSettings({ ...status.settings, channel: e.target.value as "stable" | "beta" }))}
          >
            <option value="stable">Stable releases</option>
            <option value="beta">Betas too</option>
          </select>
        </div>
        <label className="flex items-start gap-2 text-sm">
          <input
            type="checkbox"
            className="mt-0.5"
            checked={status.canApply && status.settings.auto}
            disabled={!status.canApply}
            onChange={(e) => run(() => api.saveUpdateSettings({ ...status.settings, auto: e.target.checked }))}
          />
          <span>
            Install new versions automatically
            <span className="block text-muted">
              {status.canApply
                ? "Reelyard downloads the new version, checks it against the release's checksums, and restarts. Downloads carry on afterwards."
                : "Not possible with this kind of install; you'll see a notice instead."}
            </span>
          </span>
        </label>
        {error && <p role="alert" className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">{error}</p>}
      </section>
    </>
  );
}
