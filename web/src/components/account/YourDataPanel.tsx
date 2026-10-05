"use client";
import { useState } from "react";
import { api, ApiError } from "@/lib/api";

// A member's own data (2026-10-05 audit, AUD-05): there was no way to see what
// SignalDeck held about an account or to leave it.
export default function YourDataPanel() {
  const [password, setPassword] = useState("");
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const remove = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.deleteAccount(password);
      window.location.replace("/");
    } catch (err) {
      setBusy(false);
      if (err instanceof ApiError && err.status === 403) setError(err.message || "That password is not right.");
      else if (err instanceof ApiError && err.status === 429) setError("Too many wrong passwords. Try again later.");
      else setError(err instanceof Error ? err.message : "The account could not be deleted. Try again.");
    }
  };

  return (
    <section className="panel w-full max-w-md" aria-label="your data">
      <div className="panel-h">
        <span>YOUR DATA</span>
      </div>
      <div className="flex flex-col gap-3 p-6 text-sm">
        <p className="m-0 leading-relaxed text-[var(--dim)]">
          Download everything SignalDeck holds about your account: your email and username, watchlist,
          calls and alert settings. Your password is never included.
        </p>
        <a href="/api/account/export" download="signaldeck-my-data.json" className="w-fit text-[var(--accent)] underline">
          Download my data (JSON)
        </a>
        <hr className="border-[var(--border)]" />
        <p className="m-0 leading-relaxed text-[var(--dim)]">
          Delete your account: your account, watchlist, calls and alert settings are erased now and
          cannot be recovered. Copies in backups age out as backups rotate.
        </p>
        {confirming ? (
          <form onSubmit={remove} className="flex flex-col gap-2">
            <label htmlFor="delete-password" className="text-xs text-[var(--dim)]">
              Type your password to confirm
            </label>
            <input
              id="delete-password"
              type="password"
              autoComplete="current-password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="rounded-lg border border-[var(--border)] bg-transparent px-3 py-2"
            />
            <div className="flex gap-2">
              <button
                type="submit"
                disabled={busy || password === ""}
                className="cursor-pointer rounded-lg border border-[var(--ask)] px-3 py-2 text-xs font-bold tracking-widest text-[var(--ask)] disabled:opacity-50"
              >
                {busy ? "DELETING…" : "DELETE PERMANENTLY"}
              </button>
              <button
                type="button"
                onClick={() => {
                  setConfirming(false);
                  setPassword("");
                  setError(null);
                }}
                className="cursor-pointer px-3 py-2 text-xs text-[var(--dim)]"
              >
                Cancel
              </button>
            </div>
          </form>
        ) : (
          <button
            type="button"
            onClick={() => {
              setConfirming(true);
              setError(null);
            }}
            className="w-fit cursor-pointer rounded-lg border border-[var(--ask)] px-3 py-2 text-xs font-bold tracking-widest text-[var(--ask)]"
          >
            DELETE MY ACCOUNT
          </button>
        )}
        {error && (
          <p role="alert" className="m-0 text-xs text-[var(--ask)]">
            {error}
          </p>
        )}
      </div>
    </section>
  );
}
