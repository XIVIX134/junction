import { useEffect, useState, type ReactNode } from "react";

export const Panel = ({ title, right, children }: { title: string; right?: ReactNode; children: ReactNode }) => (
  <section className="pn">
    <div className="hd">
      <h2>{title}</h2>
      {right && <div className="r">{right}</div>}
    </div>
    <div className="bd" style={{ paddingTop: 2, paddingBottom: 2 }}>
      {children}
    </div>
  </section>
);

export const Row = ({ k, hint, children }: { k: string; hint?: string; children: ReactNode }) => (
  <div className="srow">
    <div className="k">
      <b>{k}</b>
      {hint && <span>{hint}</span>}
    </div>
    <div className="v">{children}</div>
  </div>
);

/** A text field that saves on Enter or when it loses focus, if it changed. */
export function Field({ value, onSave, width = 260, label, placeholder }: { value: string; onSave: (v: string) => void; width?: number; label: string; placeholder?: string }) {
  const [v, setV] = useState(value);
  useEffect(() => setV(value), [value]);
  const commit = () => v.trim() !== value && onSave(v.trim());
  return (
    <input
      className="inp"
      style={{ width }}
      aria-label={label}
      placeholder={placeholder}
      value={v}
      onChange={(e) => setV(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => e.key === "Enter" && (e.currentTarget as HTMLInputElement).blur()}
    />
  );
}

/** A whole-number field with a unit, saved like Field when it's valid. */
export function NumField({ value, onSave, label, min, max, unit }: { value: number; onSave: (v: number) => void; label: string; min: number; max: number; unit: string }) {
  const [v, setV] = useState(String(value));
  useEffect(() => setV(String(value)), [value]);
  const n = Number(v);
  const ok = Number.isInteger(n) && n >= min && n <= max;
  return (
    <span style={{ display: "inline-flex", gap: 6, alignItems: "center" }}>
      <input
        className="inp num"
        style={{ width: 70, borderColor: ok ? undefined : "var(--bad)" }}
        aria-label={label}
        inputMode="numeric"
        title={ok ? undefined : `${min} to ${max}`}
        value={v}
        onChange={(e) => setV(e.target.value)}
        onBlur={() => (ok ? n !== value && onSave(n) : setV(String(value)))}
        onKeyDown={(e) => e.key === "Enter" && (e.currentTarget as HTMLInputElement).blur()}
      />
      <span className="t3">{unit}</span>
    </span>
  );
}

export function Seg<T extends string | number>({ value, options, onPick, label }: { value: T; options: [T, string][]; onPick: (v: T) => void; label: string }) {
  return (
    <div className="seg" role="group" aria-label={label}>
      {options.map(([k, l]) => (
        <button key={String(k)} type="button" className={value === k ? "on" : ""} aria-pressed={value === k} onClick={() => value !== k && onPick(k)}>
          {l}
        </button>
      ))}
    </div>
  );
}
