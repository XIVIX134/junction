// App-wide touches that need no change in each screen:
//  - every `title` becomes the app's own tooltip (styled, quick, keyboard too);
//    lines after the first are details. The text stays for screen readers.
//  - segmented controls and tab bars get a highlight that slides to the choice.

import { useEffect, useState } from "react";
import { reducedMotion } from "../charts";

type Tip = { text: string; x: number; y: number; below: boolean };

function tipTarget(el: EventTarget | null): HTMLElement | SVGElement | null {
  let n = el instanceof Element ? el : null;
  while (n && n !== document.body) {
    if (n.hasAttribute("data-tip")) return n as HTMLElement;
    if (n.hasAttribute("title") && n.getAttribute("title")) {
      // Take the title over (the browser would show its own box too).
      const t = n.getAttribute("title")!;
      n.removeAttribute("title");
      n.setAttribute("data-tip", t);
      if (!n.getAttribute("aria-label") && !n.getAttribute("aria-description"))
        n.setAttribute("aria-description", t);
      return n as HTMLElement;
    }
    n = n.parentElement;
  }
  return null;
}

export function Enhance() {
  const [tip, setTip] = useState<Tip | null>(null);

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    let cur: Element | null = null;
    const show = (el: Element, delay: number) => {
      clearTimeout(timer);
      cur = el;
      timer = setTimeout(() => {
        if (cur !== el || !el.isConnected) return;
        const r = el.getBoundingClientRect();
        const below = r.top < 70;
        setTip({
          text: el.getAttribute("data-tip") ?? "",
          x: r.left + r.width / 2,
          y: below ? r.bottom + 8 : r.top - 8,
          below,
        });
      }, delay);
    };
    const hide = () => {
      clearTimeout(timer);
      cur = null;
      setTip(null);
    };
    const over = (e: PointerEvent) => {
      const t = tipTarget(e.target);
      if (t === cur) return;
      if (!t) return hide();
      show(t, cur ? 60 : 280); // moving from tip to tip feels instant
    };
    const focus = (e: FocusEvent) => {
      const t = tipTarget(e.target);
      if (t && (e.target as Element).matches(":focus-visible")) show(t, 0);
    };
    document.addEventListener("pointerover", over);
    document.addEventListener("focusin", focus);
    document.addEventListener("focusout", hide);
    document.addEventListener("pointerdown", hide);
    document.addEventListener("keydown", (e) => e.key === "Escape" && hide());
    window.addEventListener("scroll", hide, true);
    return () => {
      document.removeEventListener("pointerover", over);
      document.removeEventListener("focusin", focus);
      document.removeEventListener("focusout", hide);
      document.removeEventListener("pointerdown", hide);
      window.removeEventListener("scroll", hide, true);
    };
  }, []);

  // Sliding highlight for .seg and [role=tablist]: one element per group, moved to the chosen item.
  useEffect(() => {
    const place = (group: HTMLElement) => {
      const on = group.querySelector<HTMLElement>(
        ":scope > button.on, :scope > button[aria-pressed='true'], :scope > [role=tab][aria-selected='true']",
      );
      let ind = group.querySelector<HTMLElement>(":scope > .slide-ind");
      if (!on) {
        if (ind) ind.style.opacity = "0";
        return;
      }
      if (!ind) {
        ind = document.createElement("i");
        ind.className = "slide-ind";
        ind.setAttribute("aria-hidden", "true");
        group.prepend(ind);
        group.classList.add("has-slide");
        ind.style.transition = "none";
      }
      ind.style.opacity = "1";
      ind.style.left = `${on.offsetLeft}px`;
      ind.style.width = `${on.offsetWidth}px`;
      if (ind.style.transition === "none")
        requestAnimationFrame(
          () => ind && (ind.style.transition = reducedMotion() ? "none" : ""),
        );
    };
    const all = () =>
      document
        .querySelectorAll<HTMLElement>(".seg, [role=tablist]")
        .forEach(place);
    all();
    let raf = 0;
    const mo = new MutationObserver(() => {
      cancelAnimationFrame(raf);
      raf = requestAnimationFrame(all);
    });
    mo.observe(document.body, {
      subtree: true,
      childList: true,
      attributes: true,
      attributeFilter: ["class", "aria-pressed", "aria-selected"],
    });
    window.addEventListener("resize", all);
    return () => {
      mo.disconnect();
      window.removeEventListener("resize", all);
    };
  }, []);

  if (!tip) return null;
  const [first, ...rest] = tip.text.split("\n");
  return (
    <div
      className={`gtip${tip.below ? " below" : ""}`}
      style={{
        left: Math.max(8, Math.min(window.innerWidth - 8, tip.x)),
        top: tip.y,
      }}
      role="tooltip"
    >
      <div>{first}</div>
      {rest.map((l, i) => (
        <div key={i} className="gtip-d">
          {l}
        </div>
      ))}
    </div>
  );
}
