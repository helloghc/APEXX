/** The three themes of the Apexx admin. Values mirror tokens.css (a test keeps them in step). */
export interface AdminTheme {
  id: "rosa" | "lavanda" | "niebla";
  label: string;
  /** The palette the theme is built from, lightest surfaces first. */
  palette: string[];
  bg: string;
  surface: string;
  fg: string;
  muted: string;
  accent: string;
  accentFg: string;
  accentSoft: string;
}

export const ADMIN_THEMES: AdminTheme[] = [
  { id: "rosa", label: "Rosa", palette: ["#ffb8e0", "#ffd2e9", "#ffeef8", "#ffffff", "#ecffff"], bg: "#ffeef8", surface: "#ffffff", fg: "#2a0f1f", muted: "#6e4560", accent: "#a81f6b", accentFg: "#ffffff", accentSoft: "#ffd2e9" },
  { id: "lavanda", label: "Lavanda", palette: ["#ffeef8", "#fae1ff", "#e5d8ff", "#c0cbff", "#c7e4ff"], bg: "#fae1ff", surface: "#ffffff", fg: "#1c1438", muted: "#564a7c", accent: "#4a37b8", accentFg: "#ffffff", accentSoft: "#e5d8ff" },
  { id: "niebla", label: "Niebla", palette: ["#e7eded", "#d3dbdb", "#d6dce0", "#dce6e7", "#bac5c9"], bg: "#e7eded", surface: "#ffffff", fg: "#16232a", muted: "#4a5c64", accent: "#2c4a54", accentFg: "#ffffff", accentSoft: "#d3dbdb" },
];

const KEY = "apexx_theme";

export function currentTheme(): AdminTheme["id"] {
  try {
    const v = window.localStorage.getItem(KEY);
    if (ADMIN_THEMES.some((t) => t.id === v)) return v as AdminTheme["id"];
  } catch {
    /* storage blocked */
  }
  return "rosa";
}

export function applyTheme(id: AdminTheme["id"]): void {
  document.documentElement.dataset.theme = id;
  try {
    window.localStorage.setItem(KEY, id);
  } catch {
    /* storage blocked */
  }
}
