import { useEffect, useState } from "react";

export type Theme = "light" | "dark" | "system";
const key = "segmentation-manager-theme";
const media = window.matchMedia("(prefers-color-scheme: dark)");
function savedTheme(): Theme {
  try {
    const value = localStorage.getItem(key);
    if (value === "light" || value === "dark") return value;
  } catch {
    /* Storage may be disabled. */
  }
  return "system";
}
export function applyTheme(theme: Theme) {
  document.documentElement.dataset.theme =
    theme === "system" ? (media.matches ? "dark" : "light") : theme;
}
applyTheme(savedTheme());

export function ThemeSelector({ access = false }: { access?: boolean }) {
  const [theme, setTheme] = useState<Theme>(savedTheme);
  useEffect(() => {
    const update = () => applyTheme(theme);
    update();
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, [theme]);
  useEffect(() => {
    const sync = (e: StorageEvent) => {
      if (e.key === key) setTheme(savedTheme());
    };
    window.addEventListener("storage", sync);
    return () => window.removeEventListener("storage", sync);
  }, []);
  return (
    <label className={`theme-selector ${access ? "access-theme" : ""}`}>
      Theme
      <select
        aria-label="Theme"
        value={theme}
        onChange={(e) => {
          const value = e.target.value as Theme;
          setTheme(value);
          try {
            localStorage.setItem(key, value);
          } catch {
            /* Current session still works. */
          }
        }}
      >
        <option value="system">System</option>
        <option value="light">Light</option>
        <option value="dark">Dark</option>
      </select>
    </label>
  );
}
