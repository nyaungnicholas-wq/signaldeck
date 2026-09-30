"use client";

import { Fragment, useEffect, useState } from "react";

// Word-by-word masked rise for the headline, then a terminal-style line that
// cycles through the platform's rules. The <h1> keeps its real text for
// screen readers via aria-label; the animated spans are decoration.

export default function KineticHeadline({ text, cycle }: { text: string; cycle: string[] }) {
  const [index, setIndex] = useState(0);

  useEffect(() => {
    if (cycle.length < 2 || window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    let id = 0;
    const start = () => {
      if (!id) id = window.setInterval(() => setIndex((i) => (i + 1) % cycle.length), 2800);
    };
    const stop = () => {
      window.clearInterval(id);
      id = 0;
    };
    const onVis = () => (document.hidden ? stop() : start());
    document.addEventListener("visibilitychange", onVis);
    start();
    return () => {
      stop();
      document.removeEventListener("visibilitychange", onVis);
    };
  }, [cycle.length]);

  const words = text.split(" ");
  return (
    <div className="kx-root">
      <h1
        aria-label={text}
        className="kx-h1 m-0 max-w-[16ch] text-[2.4rem] font-extrabold leading-[1.02] tracking-[-0.02em] sm:text-[3.6rem] lg:text-[4.6rem]"
      >
        {words.map((word, i) => (
          <Fragment key={i}>
            <span className="kx-word-wrap" aria-hidden="true">
              <span
                className={`kx-word${/public/i.test(word) ? " kx-accent" : ""}`}
                style={{ animationDelay: `${120 + i * 70}ms` }}
              >
                {word}
              </span>
            </span>
            {i < words.length - 1 ? " " : null}
          </Fragment>
        ))}
      </h1>
      <p className="kx-cycle-line mono mt-5 text-[0.95rem] sm:text-[1.1rem]" aria-hidden="true">
        <span className="kx-prompt">{">"}</span>
        <span className="kx-cycle-slot">
          <span key={index} className="kx-cycle-item">
            {cycle[index]}
          </span>
        </span>
        <span className="kx-caret" />
      </p>
      <ul className="sr-only">
        {cycle.map((c) => (
          <li key={c}>{c}</li>
        ))}
      </ul>
    </div>
  );
}
