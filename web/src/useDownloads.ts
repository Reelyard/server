import { useEffect, useState } from "react";
import { api, type Download } from "./api";

// Live download list: one fetch, then updates over Server-Sent Events.
// Falls back to polling every 5 s if the stream can't connect.
export function useDownloads() {
  const [downloads, setDownloads] = useState<Download[] | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    let live = true;
    let poll: number | undefined;
    const load = () =>
      api
        .downloads()
        .then((r) => live && (setDownloads(r.downloads), setError("")))
        .catch((e) => live && setError(e.message));
    load();

    const es = new EventSource("/api/v1/stream");
    es.addEventListener("downloads", (ev) => {
      if (!live) return;
      try {
        setDownloads(JSON.parse((ev as MessageEvent).data));
        setError("");
      } catch {
        /* ignore a malformed event */
      }
    });
    es.onerror = () => {
      if (poll === undefined) poll = window.setInterval(load, 5000);
    };
    es.onopen = () => {
      if (poll !== undefined) {
        window.clearInterval(poll);
        poll = undefined;
      }
    };
    return () => {
      live = false;
      es.close();
      if (poll !== undefined) window.clearInterval(poll);
    };
  }, []);

  return { downloads, error };
}
