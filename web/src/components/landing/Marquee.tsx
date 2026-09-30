import React from 'react';

export default function Marquee({ items, speed = 38 }: { items: string[]; speed?: number }) {
  return (
    <>
      <div className="mq-root" aria-hidden="true">
        <div className="mq-track" style={{ animationDuration: `${speed}s` }}>
          {[...items, ...items].map((t, i) => (
            <span className="mq-item mono" key={i}>
              <span className="mq-dot" />{t}
            </span>
          ))}
        </div>
      </div>
      <ul className="sr-only">
        {items.map(t => (
          <li key={t}>{t}</li>
        ))}
      </ul>
    </>
  );
}