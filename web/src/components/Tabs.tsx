import { useRef, type KeyboardEvent, type ReactNode } from "react";
import { useSearchParams } from "react-router";

export type Tab = { id: string; label: string; count?: number };

// useTab keeps the chosen tab in the address (?tab=…), so Back, reloads and
// shared links keep it.
export function useTab(tabs: Tab[], fallback = tabs[0]?.id ?? "") {
  const [params, setParams] = useSearchParams();
  const want = params.get("tab");
  const current = tabs.some((t) => t.id === want) ? want! : fallback;
  const choose = (id: string) =>
    setParams(
      (p) => {
        const next = new URLSearchParams(p);
        if (id === fallback) next.delete("tab");
        else next.set("tab", id);
        return next;
      },
      { replace: true },
    );
  return [current, choose] as const;
}

// Tabs is an accessible tab bar (arrow keys move between tabs).
export function Tabs({ tabs, current, onChange, label }: { tabs: Tab[]; current: string; onChange: (id: string) => void; label: string }) {
  const refs = useRef<(HTMLButtonElement | null)[]>([]);
  function key(e: KeyboardEvent, i: number) {
    let j = -1;
    if (e.key === "ArrowRight") j = (i + 1) % tabs.length;
    else if (e.key === "ArrowLeft") j = (i - 1 + tabs.length) % tabs.length;
    else if (e.key === "Home") j = 0;
    else if (e.key === "End") j = tabs.length - 1;
    if (j < 0) return;
    e.preventDefault();
    onChange(tabs[j].id);
    refs.current[j]?.focus();
  }
  return (
    <div role="tablist" aria-label={label} className="-mx-1 flex gap-1 overflow-x-auto overflow-y-hidden border-b border-line px-1">
      {tabs.map((t, i) => {
        const on = t.id === current;
        return (
          <button
            key={t.id}
            ref={(el) => {
              refs.current[i] = el;
            }}
            role="tab"
            id={`tab-${t.id}`}
            aria-selected={on}
            aria-controls={`panel-${t.id}`}
            tabIndex={on ? 0 : -1}
            onClick={() => onChange(t.id)}
            onKeyDown={(e) => key(e, i)}
            className={`-mb-px flex shrink-0 items-center gap-1.5 border-b-2 px-3 py-2 text-sm whitespace-nowrap transition-colors ${
              on ? "border-deep font-medium text-ink" : "border-transparent text-muted hover:text-ink"
            }`}
          >
            {t.label}
            {t.count !== undefined && (
              <span className={`rounded-full px-1.5 text-xs tabular-nums ${on ? "bg-deep-soft text-deep" : "bg-bg text-muted"}`}>{t.count}</span>
            )}
          </button>
        );
      })}
    </div>
  );
}

export function TabPanel({ id, children }: { id: string; children: ReactNode }) {
  return (
    <div role="tabpanel" id={`panel-${id}`} aria-labelledby={`tab-${id}`} className="flex flex-col gap-6">
      {children}
    </div>
  );
}
