import type { Brand } from "../api/types";

/** The console's icon: your image, or a letter on a colour (Settings → General). */
export function BrandMark({ b, size = 28 }: { b: Pick<Brand, "color" | "letter" | "image">; size?: number }) {
  const r = Math.round(size * 0.25);
  if (b.image) return <img src={b.image} alt="" width={size} height={size} style={{ borderRadius: r, objectFit: "cover", display: "block" }} />;
  return (
    <span
      aria-hidden="true"
      style={{
        width: size,
        height: size,
        borderRadius: r,
        background: b.color,
        color: "#fff",
        display: "grid",
        placeItems: "center",
        fontWeight: 700,
        fontSize: Math.round(size * 0.46),
        lineHeight: 1,
        flex: "none",
      }}
    >
      {b.letter.slice(0, 2)}
    </span>
  );
}
