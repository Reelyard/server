import { useState } from "react";
import { NavLink, Outlet } from "react-router";
import { api, type User } from "../api";

const NAV = [
  { to: "/", label: "Dashboard", end: true },
  { to: "/titles", label: "Titles" },
  { to: "/downloads", label: "Downloads" },
  { to: "/suppliers", label: "Suppliers" },
  { to: "/profiles", label: "Quality profiles" },
  { to: "/activity", label: "Activity" },
  { to: "/settings", label: "Settings" },
];

export function Logo({ className = "h-7 w-7" }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" className={className} aria-hidden="true">
      <rect width="32" height="32" rx="7" fill="var(--hb-deep)" />
      <path d="M16 6v16M9 15c0 5 3 8 7 8s7-3 7-8" stroke="var(--hb-surface)" strokeWidth="2.5" fill="none" strokeLinecap="round" />
      <circle cx="16" cy="7" r="2.5" fill="var(--hb-accent)" />
    </svg>
  );
}

export default function Layout({ user, onSignOut }: { user: User; onSignOut: () => void }) {
  const [open, setOpen] = useState(false);

  async function signOut() {
    try {
      await api.logout();
    } finally {
      onSignOut();
    }
  }

  return (
    <div className="flex min-h-full flex-col md:flex-row">
      <aside className="border-b border-line bg-surface md:sticky md:top-0 md:flex md:h-screen md:w-60 md:flex-col md:border-r md:border-b-0">
        <div className="flex items-center justify-between gap-3 px-4 py-3 md:px-5 md:py-5">
          <div className="flex items-center gap-2.5">
            <Logo />
            <span className="text-lg font-semibold tracking-tight">Harbor</span>
          </div>
          <button className="btn-ghost px-3 py-1.5 md:hidden" aria-expanded={open} aria-controls="nav" onClick={() => setOpen((o) => !o)}>
            Menu
          </button>
        </div>
        <nav id="nav" className={`${open ? "flex" : "hidden"} flex-col gap-0.5 px-2 pb-3 md:flex md:flex-1 md:px-3`}>
          {NAV.map((n) => (
            <NavLink
              key={n.to}
              to={n.to}
              end={n.end}
              onClick={() => setOpen(false)}
              className={({ isActive }) =>
                `rounded-lg px-3 py-2 text-sm transition-colors ${isActive ? "bg-deep-soft font-medium text-deep" : "text-ink hover:bg-bg"}`
              }
            >
              {n.label}
            </NavLink>
          ))}
        </nav>
        <div className={`${open ? "flex" : "hidden"} items-center justify-between gap-2 border-t border-line px-5 py-3 text-sm md:flex`}>
          <span className="truncate text-muted" title={user.username}>{user.username}</span>
          <button className="text-deep hover:underline" onClick={signOut}>Sign out</button>
        </div>
      </aside>
      <main className="min-w-0 flex-1 px-4 py-6 md:px-10 md:py-10">
        <div className="mx-auto max-w-5xl">
          <Outlet />
        </div>
      </main>
    </div>
  );
}
