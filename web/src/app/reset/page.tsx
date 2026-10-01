"use client";
import { Suspense, useState } from "react";
import { useSearchParams, useRouter } from "next/navigation";
import Link from "next/link";
import { api, ApiError } from "@/lib/api";

export default function ResetPage() {
  return (
    <Suspense fallback={null}>
      <ResetInner />
    </Suspense>
  );
}

function ResetInner() {
  const searchParams = useSearchParams();
  const router = useRouter();
  const token = searchParams.get("token") ?? "";
  const [busy, setBusy] = useState(false);
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [error, setError] = useState<string | null>(token ? null : "This reset link is missing its token.");
  const [success, setSuccess] = useState<string | null>(null);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setSuccess(null);
    if (!password || password.length < 8 || password.length > 72) {
      setError("Password must be 8-72 characters");
      return;
    }
    if (password !== confirmPassword) {
      setError("Passwords do not match");
      return;
    }
    setBusy(true);
    try {
      const me = await api.resetPassword(token, password);
      setSuccess("Password updated. Signing you in…");
      setTimeout(() => {
        router.replace(me.isAdmin ? "/dashboard" : "/today");
      }, 1200);
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message);
      } else {
        setError("An unexpected error occurred");
      }
      // Re-enable only on failure: on success the button must stay disabled
      // through the redirect, or a second click spends the used token and
      // paints an error over "Password updated".
      setBusy(false);
    }
  };

  if (!token) {
    return (
      <div className="flex min-h-[70vh] flex-col items-center justify-center gap-4 px-4">
        <form className="panel w-full max-w-sm">
          <div className="panel-h">
            <span aria-hidden="true" className="inline-block h-2 w-2 shrink-0 rounded-full" style={{ background: "var(--accent)" }} />
            <span className="mono tracking-[0.22em]">SIGNALDECK</span>
          </div>
          <div className="p-6">
            <h1 className="mb-6 text-lg font-bold tracking-widest text-[var(--text)]">NEW PASSWORD</h1>
            <div role="alert" className="mb-4 rounded-lg border border-[var(--ask)] bg-[var(--ask-dim)] px-3 py-2 text-xs leading-relaxed text-[var(--ask)]">
              This reset link is missing its token.
            </div>
            <Link href="/forgot" className="text-xs text-[var(--dim)] transition-colors duration-150 hover:text-[var(--accent)]">
              Request a new link
            </Link>
          </div>
        </form>
      </div>
    );
  }

  return (
    <div className="flex min-h-[70vh] flex-col items-center justify-center gap-4 px-4">
      <form className="panel w-full max-w-sm" onSubmit={handleSubmit}>
        <div className="panel-h">
          <span aria-hidden="true" className="inline-block h-2 w-2 shrink-0 rounded-full" style={{ background: "var(--accent)" }} />
          <span className="mono tracking-[0.22em]">SIGNALDECK</span>
        </div>
        <div className="p-6">
          <h1 className="mb-6 text-lg font-bold tracking-widest text-[var(--text)]">NEW PASSWORD</h1>
          {error && (
            <div role="alert" className="mb-4 rounded-lg border border-[var(--ask)] bg-[var(--ask-dim)] px-3 py-2 text-xs leading-relaxed text-[var(--ask)]">
              {error}
            </div>
          )}
          {success && (
            <div role="status" className="mb-4 rounded-lg border border-[var(--bid)] bg-[var(--bid-dim)] px-3 py-2 text-xs leading-relaxed text-[var(--bid)]">
              {success}
            </div>
          )}
          <div>
            <label className="mb-1 block text-xs tracking-wider text-[var(--dim)]">PASSWORD</label>
            <input
              type="password"
              autoComplete="new-password"
              className="mono mb-4 w-full rounded-lg border border-[var(--border)] bg-[var(--panel2)] px-3 py-2 text-sm text-[var(--text)] outline-none transition-colors duration-150 focus:border-[var(--accent)]"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
            />
          </div>
          <div>
            <label className="mb-1 block text-xs tracking-wider text-[var(--dim)]">CONFIRM PASSWORD</label>
            <input
              type="password"
              autoComplete="new-password"
              className="mono mb-4 w-full rounded-lg border border-[var(--border)] bg-[var(--panel2)] px-3 py-2 text-sm text-[var(--text)] outline-none transition-colors duration-150 focus:border-[var(--accent)]"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              required
            />
          </div>
          <button
            type="submit"
            disabled={busy}
            className="w-full cursor-pointer rounded-lg border border-[var(--accent)] bg-transparent px-3 py-2 text-sm font-bold tracking-widest text-[var(--accent)] transition-colors duration-150 hover:bg-[var(--accent)] hover:text-[var(--bg)] disabled:cursor-not-allowed disabled:opacity-40"
          >
            {busy ? "SAVING…" : "SET PASSWORD"}
          </button>
        </div>
      </form>
    </div>
  );
}