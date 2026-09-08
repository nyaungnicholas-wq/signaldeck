"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

const ITEMS = [
  { label: "Grades", href: "/accuracy" },
  { label: "Risk estimates", href: "/volatility" },
  { label: "Receipts", href: "/proof" },
  { label: "Glossary", href: "/glossary" },
] as const;

export default function PublicNav({ pathname }: { pathname?: string }) {
  const routed = usePathname();
  const current = pathname ?? routed;
  const isActive = (href: string) =>
    current === href || current.startsWith(href + "/");

  return (
    <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2">
      <nav aria-label="Public record" className="flex flex-wrap items-center gap-1">
        {ITEMS.map(({ label, href }) => {
          const active = isActive(href);
          return (
            <Link
              key={href}
              href={href}
              aria-current={active ? "page" : undefined}
              className={`nav-link flex min-h-[40px] items-center px-3 py-2 text-[0.75rem] font-medium tracking-[0.06em]${active ? " nav-link-active" : ""}`}
            >
              {label}
            </Link>
          );
        })}
      </nav>
      {current !== "/login" && (
        <Link href="/login" className="chip ml-auto">
          Sign in
        </Link>
      )}
    </div>
  );
}