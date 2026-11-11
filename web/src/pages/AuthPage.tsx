import { useState, type FormEvent } from "react";
import { api, type User } from "../api";
import { Logo } from "../components/Layout";

export default function AuthPage({ mode, onDone }: { mode: "setup" | "login"; onDone: (u: User) => void }) {
  const [username, setUsername] = useState(mode === "setup" ? "admin" : "");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const setup = mode === "setup";

  async function submit(e: FormEvent) {
    e.preventDefault();
    setError("");
    if (setup && password !== confirm) return setError("The two passwords don't match.");
    setBusy(true);
    try {
      onDone(setup ? await api.setup(username, password) : await api.login(username, password));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Something went wrong.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="grid min-h-full place-items-center px-4 py-10">
      <form onSubmit={submit} className="card w-full max-w-sm p-6 sm:p-8" noValidate>
        <div className="mb-6 flex items-center gap-3">
          <Logo className="h-9 w-9" />
          <div>
            <h1 className="text-xl font-semibold">{setup ? "Welcome to Reelyard" : "Sign in to Reelyard"}</h1>
            <p className="text-sm text-muted">{setup ? "Create the admin account to get started." : "Your media tracker and downloader."}</p>
          </div>
        </div>
        <div className="flex flex-col gap-4">
          <div>
            <label className="label" htmlFor="username">Username</label>
            <input id="username" className="input" autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} required autoFocus={!setup} />
          </div>
          <div>
            <label className="label" htmlFor="password">Password</label>
            <input id="password" type="password" className="input" autoComplete={setup ? "new-password" : "current-password"} value={password} onChange={(e) => setPassword(e.target.value)} required autoFocus={setup} />
            {setup && <p className="mt-1 text-xs text-muted">At least 8 characters.</p>}
          </div>
          {setup && (
            <div>
              <label className="label" htmlFor="confirm">Repeat password</label>
              <input id="confirm" type="password" className="input" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required />
            </div>
          )}
          {error && <p role="alert" className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">{error}</p>}
          <button className="btn-primary mt-1 w-full" disabled={busy || !username || !password}>
            {busy ? "Please wait…" : setup ? "Create account" : "Sign in"}
          </button>
        </div>
      </form>
    </div>
  );
}
