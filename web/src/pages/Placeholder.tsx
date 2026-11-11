export default function Placeholder({ title, text, milestone }: { title: string; text: string; milestone: string }) {
  return (
    <div className="flex flex-col gap-6">
      <header>
        <p className="eyebrow">{title}</p>
        <h1 className="mt-1 text-2xl font-semibold tracking-tight">{title}</h1>
      </header>
      <div className="card flex flex-col items-start gap-2 border-dashed p-8">
        <span className="rounded-full bg-accent-soft px-2.5 py-0.5 text-xs font-medium text-accent">Coming in {milestone}</span>
        <p className="max-w-prose text-muted">{text}</p>
      </div>
    </div>
  );
}
