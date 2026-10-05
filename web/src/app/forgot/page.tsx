"use client";
import { useState } from "react";
import Link from "next/link";
import { api, ApiError } from "@/lib/api";
import Turnstile from "@/components/auth/Turnstile";

export default function ForgotPage() {
  const [email, setEmail] = useState("");
  const [turnstileToken, setTurnstileToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [sent, setSent] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      await api.forgotPassword(email.trim(), turnstileToken);
      setSent(true);
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message);
      } else {
        setError("request failed");
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex min-h-[70vh] flex-col items-center justify-center gap-4 px-4">
      <form className="panel w-full max-w-sm" onSubmit={handleSubmit}>
        <div className="panel-h">
          <span aria-hidden="true" className="inline-block h-2 w-2 shrink-0 rounded-full" style={{ background: "var(--accent)" }} />
          <span className="mono tracking-[0.22em]">SIGNALDECK</span>
        </div>
        <div className="p-6">
          <h1 className="mb-6 text-lg font-bold tracking-widest text-[var(--text)]">RESET PASSWORD</h1>
          <label className="mb-1 block text-xs tracking-wider text-[var(--dim)]">EMAIL</label>
          <input
            type="email"
            autoComplete="email"
            required
            className="mono mb-4 w-full rounded-lg border border-[var(--border)] bg-[var(--panel2)] px-3 py-2 text-sm text-[var(--text)] outline-none transition-colors duration-150 focus:border-[var(--accent)]"
            value={email}
            onChange={e => setEmail(e.target.value)}
          />
          <Turnstile onToken={setTurnstileToken} />
          <button
            type="submit"
            disabled={busy}
            className="w-full cursor-pointer rounded-lg border border-[var(--accent)] bg-transparent px-3 py-2 text-sm font-bold tracking-widest text-[var(--accent)] transition-colors duration-150 hover:bg-[var(--accent)] hover:text-[var(--bg)] disabled:cursor-not-allowed disabled:opacity-40"
          >
            {busy ? "SENDING…" : "SEND RESET LINK"}
          </button>
          {error && (
            <div className="mb-4 rounded-lg border border-[var(--ask)] bg-[var(--ask-dim)] px-3 py-2 text-xs leading-relaxed text-[var(--ask)]" role="alert">
              {error}
            </div>
          )}
          {sent && (
            <div className="mb-4 rounded-lg border border-[var(--bid)] bg-[var(--bid-dim)] px-3 py-2 text-xs leading-relaxed text-[var(--bid)]" role="status">
              If an account uses that email, a reset link is on its way. It expires in 1 hour.
            </div>
          )}
        </div>
      </form>
      <Link
        href="/login"
        className="text-xs text-[var(--dim)] transition-colors duration-150 hover:text-[var(--accent)]"
      >
        Back to sign in
      </Link>
    </div>
  );
}