// Small display helpers shared by pages.

export function bytes(n?: number) {
  if (!n || n <= 0) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 100 || i < 2 ? v.toFixed(0) : v.toFixed(1)} ${units[i]}`;
}

export function ago(iso?: string) {
  if (!iso) return "never";
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return `${Math.floor(s / 86400)} d ago`;
}

const LANG: Record<string, string> = {
  en: "English", pt: "Portuguese", "pt-BR": "Portuguese (BR)", es: "Spanish", "es-419": "Spanish (LatAm)",
  fr: "French", de: "German", it: "Italian", ru: "Russian", uk: "Ukrainian", pl: "Polish", nl: "Dutch",
  ja: "Japanese", ko: "Korean", zh: "Chinese", hi: "Hindi", tr: "Turkish", ar: "Arabic", sv: "Swedish",
  cs: "Czech", hu: "Hungarian", ro: "Romanian", el: "Greek", he: "Hebrew", da: "Danish", fi: "Finnish", no: "Norwegian",
};

export function langName(code: string) {
  return LANG[code] ?? code;
}

export function episodeCode(season?: number, episode?: number) {
  const p = (n: number) => String(n).padStart(2, "0");
  return season !== undefined && episode !== undefined ? `S${p(season)}E${p(episode)}` : "";
}
