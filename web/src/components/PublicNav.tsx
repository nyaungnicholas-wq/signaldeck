"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useState } from "react";
import { api } from "@/lib/api";

const ITEMS = [
  { label: "Grades", href: "/accuracy" },
  // NOT "Risk estimates". /volatility is the graded record of ONE
  // pre-registered volatility forecast against two baselines, both horizons
  // currently INSUFFICIENT. It has no per-symbol risk lookup and no loss
  // estimate in anyone's money -- the "loss" it reports is QLIKE, the function
  // the forecast is scored under. A nav label is the first promise a visitor
  // reads, and this one was promising a tool that does not exist.
  { label: "Volatility record", href: "/volatility" },
  { label: "Receipts", href: "/proof" },
  { label: "Glossary", href: "/glossary" },
] as const;

export default function PublicNav({ pathname }: { pathname?: string }) {
  const routed = usePathname();
  const current = pathname ?? routed;
  const isActive = (href: string) =>
    current === href || current.startsWith(href + "/");
  // "Forecasts" is offered only where the daemon says its price source may be
  // shown publicly; elsewhere the link would lead to a refusal.
  const [forecasts, setForecasts] = useState(false);
  // The IEX-fed public instance needs no account, so it offers none.
  const [noAccounts, setNoAccounts] = useState(false);
  useEffect(() => {
    let alive = true;
    api
      .health()
      .then((h) => {
        if (!alive) return;
        setForecasts(!!h?.priceSource);
        setNoAccounts(h?.priceSource === "iexhist");
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, []);
  // The receipts page verifies a hash chain the public instance has not begun
  // (its forecasts are frozen for grading, not chained), so it is not offered there.
  const base = noAccounts ? ITEMS.filter((i) => i.href !== "/proof") : ITEMS;
  const items = forecasts ? [{ label: "Forecasts", href: "/forecasts" }, ...base] : base;

  return (
    <div className="flex min-w-0 basis-full flex-wrap items-center gap-2 sm:flex-1 sm:basis-auto">
      <nav aria-label="Public record" className="flex flex-wrap items-center gap-1">
        {items.map(({ label, href }) => {
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
      <div className="ml-auto flex items-center gap-2">
        {!noAccounts && current !== "/login" && (
          <Link href="/login" className="chip">
            Sign in
          </Link>
        )}
        {!noAccounts && current !== "/signup" && (
          <Link href="/signup" className="chip chip-cta">
            Sign up
          </Link>
        )}
      </div>
    </div>
  );
}