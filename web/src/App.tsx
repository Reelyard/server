import { useCallback, useEffect, useState } from "react";
import { Navigate, Route, Routes, useNavigate } from "react-router";
import { api, type User } from "./api";
import Layout from "./components/Layout";
import AuthPage from "./pages/AuthPage";
import Activity from "./pages/Activity";
import Dashboard from "./pages/Dashboard";
import Downloads from "./pages/Downloads";
import Profiles from "./pages/Profiles";
import Search from "./pages/Search";
import Settings from "./pages/Settings";
import Suppliers from "./pages/Suppliers";
import Titles from "./pages/Titles";
import Title from "./pages/Title";
import Welcome from "./pages/Welcome";

type Phase = { kind: "loading" } | { kind: "error"; message: string } | { kind: "setup" } | { kind: "login" } | { kind: "ready"; user: User };

export default function App() {
  const [phase, setPhase] = useState<Phase>({ kind: "loading" });
  // First-run setup: null while checking.
  const [welcome, setWelcome] = useState<boolean | null>(null);
  const navigate = useNavigate();

  useEffect(() => {
    if (phase.kind !== "ready") return;
    setWelcome(null);
    api.onboarding().then((o) => setWelcome(!o.done)).catch(() => setWelcome(false));
  }, [phase.kind]);

  const load = useCallback(async () => {
    try {
      if (await api.setupNeeded()) return setPhase({ kind: "setup" });
      try {
        setPhase({ kind: "ready", user: await api.me() });
      } catch {
        setPhase({ kind: "login" });
      }
    } catch {
      setPhase({ kind: "error", message: "Harbor's server isn't answering. Check that it is running, then reload." });
    }
  }, []);

  useEffect(() => {
    load();
    const onSignedOut = () => setPhase((p) => (p.kind === "ready" ? { kind: "login" } : p));
    window.addEventListener("harbor:signed-out", onSignedOut);
    return () => window.removeEventListener("harbor:signed-out", onSignedOut);
  }, [load]);

  switch (phase.kind) {
    case "loading":
      return <div className="grid h-full place-items-center text-muted">Loading Harbor…</div>;
    case "error":
      return (
        <div className="grid h-full place-items-center p-6">
          <div className="card max-w-md p-6 text-center">
            <p className="mb-4">{phase.message}</p>
            <button className="btn-primary" onClick={load}>Try again</button>
          </div>
        </div>
      );
    case "setup":
      return <AuthPage mode="setup" onDone={(user) => setPhase({ kind: "ready", user })} />;
    case "login":
      return <AuthPage mode="login" onDone={(user) => setPhase({ kind: "ready", user })} />;
    case "ready":
      if (welcome === null) return <div className="grid h-full place-items-center text-muted">Loading Harbor…</div>;
      if (welcome) return <Welcome onFinished={(to) => (setWelcome(false), navigate(to))} />;
      return (
        <Routes>
          <Route element={<Layout user={phase.user} onSignOut={() => setPhase({ kind: "login" })} />}>
            <Route index element={<Dashboard />} />
            <Route path="titles" element={<Titles />} />
            <Route path="search" element={<Search />} />
            <Route path="title/:type/:id" element={<Title />} />
            <Route path="suppliers" element={<Suppliers />} />
            <Route path="profiles" element={<Profiles />} />
            <Route path="downloads" element={<Downloads />} />
            <Route path="activity" element={<Activity />} />
            <Route path="settings" element={<Settings user={phase.user} />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Route>
        </Routes>
      );
  }
}
