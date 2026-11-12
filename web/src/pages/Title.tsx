import { useEffect, useMemo, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router";
import { ApiError, api, type Candidate, type Item, type Meta, type Profile, type StreamSearch, type Tracked } from "../api";
import { bytes, episodeCode, langName } from "../format";
import LibraryFiles from "../components/LibraryFiles";
import TrackPanel from "../components/TrackPanel";
import { TitleViewers } from "../components/Viewers";
import { Poster } from "./Search";

const RES_ORDER = ["2160p", "1080p", "720p", "576p", "480p", "360p"];

function Chip({ children, tone = "plain", title }: { children: React.ReactNode; tone?: "plain" | "deep" | "accent" | "bad"; title?: string }) {
  const cls = {
    plain: "bg-bg text-ink",
    deep: "bg-deep-soft text-deep",
    accent: "bg-accent-soft text-accent",
    bad: "bg-bad-soft text-bad",
  }[tone];
  return <span title={title} className={`inline-block rounded px-1.5 py-0.5 text-xs whitespace-nowrap ${cls}`}>{children}</span>;
}

type TitleRef = { type: string; id: string; name: string };

function DownloadButton({ c, title }: { c: Candidate; title: TitleRef }) {
  const [state, setState] = useState<"idle" | "busy" | "added" | "exists">("idle");
  const [error, setError] = useState("");
  if (!c.usable || !c.infoHash) return null;

  async function add() {
    setState("busy");
    setError("");
    try {
      await api.addDownload({
        infoHash: c.infoHash!,
        fileIdx: c.fileIdx,
        sources: c.sources,
        name: c.stream.torrentName,
        titleType: title.type,
        titleId: title.id,
        titleName: title.name,
        seeders: c.stream.seeders >= 0 ? c.stream.seeders : undefined,
      });
      setState("added");
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) return setState("exists");
      setError(e instanceof Error ? e.message : "Couldn't start the download.");
      setState("idle");
    }
  }

  if (state === "added" || state === "exists") {
    return (
      <Link to="/downloads" className="rounded-lg bg-ok-soft px-3 py-1 text-sm text-ok hover:underline">
        {state === "added" ? "Added · view" : "Already in downloads"}
      </Link>
    );
  }
  return (
    <span className="flex items-center gap-2">
      {error && <span role="alert" className="text-xs text-bad">{error}</span>}
      <button className="btn-primary py-1" disabled={state === "busy"} onClick={add}>{state === "busy" ? "Adding…" : "Download"}</button>
    </span>
  );
}

function CandidateRow({ c, title }: { c: Candidate; title: TitleRef }) {
  const i = c.stream.info;
  const ok = c.eval ? c.eval.accepted : !c.problems || c.problems.length === 0;
  return (
    <li className={`flex flex-col gap-2 px-4 py-3 ${ok ? "" : "opacity-60"}`}>
      <div className="flex flex-col gap-1 sm:flex-row sm:items-start sm:justify-between sm:gap-4">
        <div className="min-w-0 flex-1">
          <p className="font-mono text-sm break-all">{c.stream.torrentName}</p>
          {c.stream.filePath && c.stream.filePath !== c.stream.torrentName && (
            <p className="font-mono text-xs break-all text-muted">↳ {c.stream.filePath}</p>
          )}
        </div>
        <div className="flex shrink-0 items-baseline gap-4 text-sm tabular-nums">
          <span title="Seeders">{c.stream.seeders >= 0 ? `${c.stream.seeders} seeders` : "seeders ?"}</span>
          <span className="sm:w-16 sm:text-right">{bytes(c.stream.size)}</span>
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-1.5">
        {i.resolution ? <Chip tone="deep">{i.resolution}</Chip> : <Chip tone="plain">size unknown</Chip>}
        {i.source && <Chip tone={i.source === "REMUX" ? "accent" : "plain"}>{i.source}</Chip>}
        {i.codec && <Chip>{i.codec}{i.bitDepth ? ` ${i.bitDepth}-bit` : ""}</Chip>}
        {i.hdr?.map((h) => <Chip key={h} tone="accent">{h}</Chip>)}
        {i.audio && i.audio.length > 0 && <Chip>{i.audio.join(" ")}{i.channels ? ` ${i.channels}` : ""}</Chip>}
        {i.languages?.map((l) => <Chip key={l} title={langName(l)}>{l.toUpperCase()}</Chip>)}
        {i.multi && <Chip>Multi audio</Chip>}
        {i.seasonPack && <Chip tone="deep">Season pack</Chip>}
        {i.upscaled && <Chip tone="bad">Upscaled</Chip>}
        <span className="ml-auto text-xs text-muted">
          {c.stream.site ? `${c.stream.site} · ` : ""}via {c.supplierName}
          {c.alsoFrom?.length ? ` (+${c.alsoFrom.join(", ")})` : ""}
        </span>
        <DownloadButton c={c} title={title} />
      </div>
      {c.eval?.accepted && (
        <p className="text-xs text-ok">Fits the profile{c.eval.notes?.length ? ` · ${c.eval.notes.join(" · ")}` : ""}</p>
      )}
      {!ok && <p className="text-xs text-bad">{(c.eval?.rejections ?? c.problems ?? []).join(" · ")}</p>}
    </li>
  );
}

function Streams({ type, id, name, profileId }: { type: string; id: string; name: string; profileId?: number }) {
  const [data, setData] = useState<StreamSearch | null>(null);
  const [error, setError] = useState("");
  const [res, setRes] = useState("");
  const [showAll, setShowAll] = useState(false);

  useEffect(() => {
    let live = true;
    setData(null);
    setError("");
    api.streams(type, id, profileId).then((d) => live && setData(d)).catch((e) => live && setError(e.message));
    return () => {
      live = false;
    };
  }, [type, id, profileId]);

  const resolutions = useMemo(() => {
    const set = new Set((data?.candidates ?? []).map((c) => c.stream.info.resolution).filter(Boolean) as string[]);
    return RES_ORDER.filter((r) => set.has(r));
  }, [data]);

  if (error) return <p role="alert" className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">{error}</p>;
  if (!data) return <p className="text-muted">Asking your suppliers…</p>;

  const fits = (c: Candidate) => (c.eval ? c.eval.accepted : !c.problems?.length);
  const good = data.candidates.filter(fits);
  const shown = data.candidates.filter((c) => (showAll || fits(c)) && (!res || c.stream.info.resolution === res));

  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        {data.suppliers.length === 0 && (
          <span className="text-muted">
            No stream suppliers are on. <Link to="/suppliers" className="text-deep underline">Add one</Link> to see torrents here.
          </span>
        )}
        {data.suppliers.map((s) => (
          <Chip key={s.id} tone={s.error ? "bad" : "plain"} title={s.error}>
            {s.name}: {s.error ? s.error : `${s.count} in ${(s.durationMs / 1000).toFixed(1)}s`}
          </Chip>
        ))}
      </div>
      {data.candidates.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="mr-1 text-sm text-muted">
            {data.profile ? `${good.length} of ${data.candidates.length} fit “${data.profile}”` : `${good.length} usable of ${data.candidates.length}`}
          </span>
          {["", ...resolutions].map((r) => (
            <button
              key={r || "all"}
              aria-pressed={res === r}
              onClick={() => setRes(r)}
              className={`rounded-full border px-2.5 py-0.5 text-xs ${res === r ? "border-ink bg-ink text-bg" : "border-line bg-surface hover:bg-bg"}`}
            >
              {r || "Any size"}
            </button>
          ))}
          <label className="ml-auto flex items-center gap-1.5 text-sm">
            <input id="show-all" type="checkbox" checked={showAll} onChange={(e) => setShowAll(e.target.checked)} className="accent-[var(--hb-deep)]" />
            {data.profile ? "Show the rest" : "Show unusable"}
          </label>
        </div>
      )}
      {data.profile && good.length === 0 && data.summary && <p className="text-sm text-muted">{data.summary}.</p>}
      {shown.length > 0 ? (
        <ul className="card divide-y divide-line">{shown.map((c) => <CandidateRow key={c.key} c={c} title={{ type, id, name }} />)}</ul>
      ) : (
        data.suppliers.length > 0 && <p className="text-muted">No streams match.</p>
      )}
      <p className="text-xs text-muted">Download fetches only the file for this title, even from a season pack. Download only what you have the rights to.</p>
    </section>
  );
}

const ITEM_STATE: Record<Item["state"], { text: string; cls: string }> = {
  unaired: { text: "Not out yet", cls: "bg-bg text-muted" },
  missing: { text: "Wanted", cls: "bg-accent-soft text-accent" },
  downloading: { text: "Downloading", cls: "bg-deep-soft text-deep" },
  imported: { text: "In library", cls: "bg-ok-soft text-ok" },
};

function StateChip({ it }: { it: Item }) {
  if (!it.monitored && it.state !== "imported") return <span className="shrink-0 rounded px-1.5 py-0.5 text-xs text-muted">Skipped</span>;
  const s = ITEM_STATE[it.state];
  return <span className={`shrink-0 rounded px-1.5 py-0.5 text-xs ${s.cls}`}>{s.text}</span>;
}

function ItemStatus({ it, onSearch }: { it: Item; onSearch: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [monitored, setMonitored] = useState(it.monitored);
  const next = it.nextSearchAt ? new Date(it.nextSearchAt) : null;

  async function run(fn: () => Promise<unknown>) {
    setBusy(true);
    setError("");
    try {
      await fn();
      setTimeout(onSearch, 1500);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Something went wrong.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="card flex flex-col gap-2 p-4 text-sm">
      <div className="flex flex-wrap items-center gap-2">
        <StateChip it={{ ...it, monitored }} />
        <span className="min-w-0 flex-1 break-words">{it.note || (it.state === "missing" ? "Waiting for the next search." : "")}</span>
      </div>
      <div className="flex flex-wrap items-center gap-3 text-xs text-muted">
        {it.lastSearchAt && <span>Last search {new Date(it.lastSearchAt).toLocaleString()}</span>}
        {it.state === "missing" && monitored && next && <span>Next {next.toLocaleString()}</span>}
        <label className="flex items-center gap-1.5">
          <input
            id={`mon-${it.id}`}
            type="checkbox"
            checked={monitored}
            disabled={busy}
            onChange={(e) => { const on = e.target.checked; setMonitored(on); run(() => api.setItemMonitored(it.id, on)); }}
            className="accent-[var(--hb-deep)]"
          />
          Get this automatically
        </label>
        {it.state === "missing" && monitored && (
          <button className="btn-ghost ml-auto py-1" disabled={busy} onClick={() => run(() => api.searchItemNow(it.id))}>Search now</button>
        )}
      </div>
      {error && <p role="alert" className="text-xs text-bad">{error}</p>}
    </div>
  );
}

export default function Title() {
  const { type = "", id = "" } = useParams();
  const [params, setParams] = useSearchParams();
  const [meta, setMeta] = useState<Meta | null>(null);
  const [error, setError] = useState("");
  const [tracked, setTracked] = useState<Tracked | null>(null);
  const [items, setItems] = useState<Item[]>([]);
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [refreshKey, setRefreshKey] = useState(0);
  const [pickError, setPickError] = useState("");
  const [busySeason, setBusySeason] = useState(false);

  useEffect(() => {
    let live = true;
    setMeta(null);
    setError("");
    api.meta(type, id).then((m) => live && setMeta(m)).catch((e) => live && setError(e.message));
    return () => {
      live = false;
    };
  }, [type, id]);

  const seasons = useMemo(() => {
    const s = new Set((meta?.videos ?? []).map((v) => v.season));
    return [...s].sort((a, b) => (a === 0 ? 1 : b === 0 ? -1 : a - b));
  }, [meta]);
  const season = Number(params.get("s") ?? seasons[0] ?? 1);
  const episodeId = params.get("ep");
  const episodes = (meta?.videos ?? []).filter((v) => v.season === season);
  const picked = meta?.videos?.find((v) => v.id === episodeId);
  const itemByID = useMemo(() => new Map(items.map((i) => [i.itemId, i])), [items]);
  const refreshItems = () => {
    if (meta) api.trackedByImdb(meta.id).then((r) => (setTracked(r.title), setItems(r.items))).catch(() => {});
  };

  if (error) return <p role="alert" className="rounded-lg bg-bad-soft px-3 py-2 text-sm text-bad">{error}</p>;
  if (!meta) return <p className="text-muted">Loading…</p>;

  const now = Date.now();
  void profiles;
  const seasonItems = episodes.map((v) => itemByID.get(v.id)).filter((x): x is Item => !!x && x.state !== "imported");
  const seasonOn = seasonItems.filter((x) => x.monitored).length;

  async function pickEpisode(it: Item, on: boolean) {
    setPickError("");
    setItems((xs) => xs.map((x) => (x.id === it.id ? { ...x, monitored: on } : x)));
    try {
      await api.setItemMonitored(it.id, on);
      setRefreshKey((k) => k + 1);
    } catch (e) {
      setItems((xs) => xs.map((x) => (x.id === it.id ? { ...x, monitored: !on } : x)));
      setPickError(e instanceof Error ? e.message : "Couldn't change that episode.");
    }
  }
  async function pickSeason(on: boolean) {
    if (!tracked) return;
    setPickError("");
    setBusySeason(true);
    try {
      const r = await api.setSeasonMonitored(tracked.id, season, on);
      setItems(r.items);
      setRefreshKey((k) => k + 1);
    } catch (e) {
      setPickError(e instanceof Error ? e.message : "Couldn't change the season.");
    } finally {
      setBusySeason(false);
    }
  }
  return (
    <div className="flex flex-col gap-8">
      <header className="flex flex-col gap-5 sm:flex-row">
        <div className="w-32 shrink-0 sm:w-40"><Poster src={meta.poster} name={meta.name} /></div>
        <div className="flex min-w-0 flex-col gap-2">
          <p className="eyebrow">{meta.type === "movie" ? "Movie" : "Series"}</p>
          <h1 className="text-2xl font-semibold tracking-tight">{meta.name} <span className="font-normal text-muted">{meta.releaseInfo || meta.year}</span></h1>
          <p className="text-sm text-muted">
            {[meta.runtime, meta.genres?.slice(0, 3).join(", "), meta.imdbRating && `IMDb ${meta.imdbRating}`, meta.status].filter(Boolean).join(" · ")}
          </p>
          {meta.description && <p className="max-w-prose text-sm">{meta.description}</p>}
          <div className="mt-1">
            <TrackPanel imdbId={meta.id} type={meta.type} seasons={seasons} refreshKey={refreshKey} onChange={(t, it, ps) => (setTracked(t), setItems(it), setProfiles(ps))} />
            <div className="mt-2">
              <TitleViewers titleId={meta.id} />
            </div>
          </div>
        </div>
      </header>

      {meta.type === "movie" && (
        <section className="flex flex-col gap-3">
          <LibraryFiles titleId={meta.id} />
          {itemByID.get(meta.id) && <ItemStatus it={itemByID.get(meta.id)!} onSearch={refreshItems} />}
          <h2 className="text-lg font-semibold">Available streams</h2>
          <Streams type="movie" id={meta.id} name={meta.name} profileId={tracked?.profileId} />
        </section>
      )}

      {meta.type === "series" && (
        <section className="flex flex-col gap-4">
          <div className="flex flex-wrap items-center gap-3">
            <h2 className="text-lg font-semibold">Episodes</h2>
            <label htmlFor="season" className="sr-only">Season</label>
            <select
              id="season"
              className="input w-auto py-1.5"
              value={season}
              onChange={(e) => setParams({ s: e.target.value })}
            >
              {seasons.map((s) => <option key={s} value={s}>{s === 0 ? "Specials" : `Season ${s}`}</option>)}
            </select>
            {tracked && seasonItems.length > 0 && (
              <span className="flex flex-wrap items-center gap-2 text-sm">
                <span className="text-muted">{seasonOn} of {seasonItems.length} to get</span>
                {seasonOn < seasonItems.length && (
                  <button className="btn-ghost py-1" disabled={busySeason} onClick={() => pickSeason(true)}>Get whole season</button>
                )}
                {seasonOn > 0 && (
                  <button className="btn-ghost py-1" disabled={busySeason} onClick={() => pickSeason(false)}>Skip season</button>
                )}
              </span>
            )}
          </div>
          {pickError && <p role="alert" className="text-sm text-bad">{pickError}</p>}
          <ol className="card divide-y divide-line">
            {episodes.map((v) => {
              const aired = new Date(v.released).getTime() <= now;
              const active = v.id === episodeId;
              const it = itemByID.get(v.id);
              return (
                <li key={v.id} className={`flex items-center ${active ? "bg-deep-soft" : ""}`}>
                  {tracked && (
                    <span className="flex w-10 shrink-0 justify-center pl-2">
                      {it && it.state !== "imported" ? (
                        <input
                          type="checkbox"
                          id={`get-${v.id}`}
                          aria-label={`Get ${episodeCode(v.season, v.episode)}`}
                          title="Get this episode automatically"
                          checked={it.monitored}
                          onChange={(e) => pickEpisode(it, e.target.checked)}
                          className="accent-[var(--hb-deep)]"
                        />
                      ) : null}
                    </span>
                  )}
                  <button
                    onClick={() => setParams({ s: String(season), ep: v.id })}
                    className={`flex min-w-0 flex-1 items-baseline gap-3 py-2.5 pr-4 text-left text-sm hover:bg-bg ${tracked ? "pl-1" : "pl-4"}`}
                    aria-current={active}
                  >
                    <span className="w-16 shrink-0 font-mono text-xs text-muted">{episodeCode(v.season, v.episode)}</span>
                    <span className="min-w-0 flex-1 truncate">{v.title || `Episode ${v.episode}`}</span>
                    {it && <StateChip it={it} />}
                    <span className={`shrink-0 text-xs ${aired ? "text-muted" : "text-accent"}`}>
                      {v.released ? (aired ? new Date(v.released).toLocaleDateString() : `Airs ${new Date(v.released).toLocaleDateString()}`) : ""}
                    </span>
                  </button>
                </li>
              );
            })}
          </ol>
          {picked ? (
            <section className="flex flex-col gap-3">
              <LibraryFiles key={picked.id} titleId={meta.id} episodeId={picked.id} />
              {itemByID.get(picked.id) && (
                <ItemStatus key={`${picked.id}-${itemByID.get(picked.id)!.monitored}`} it={itemByID.get(picked.id)!} onSearch={() => (refreshItems(), setRefreshKey((k) => k + 1))} />
              )}
              <h3 className="font-semibold">Streams for {episodeCode(picked.season, picked.episode)} · {picked.title}</h3>
              <Streams type="series" id={picked.id} name={meta.name} profileId={tracked?.profileId} />
            </section>
          ) : (
            <p className="text-muted">Pick an episode to see the streams your suppliers have for it.</p>
          )}
        </section>
      )}
    </div>
  );
}
