"use client";

import { useState, useEffect, useRef } from "react";
import Link from "next/link";
import { api, ApiError } from "@/lib/api";
import Turnstile from "@/components/auth/Turnstile";
import GoogleButton from "@/components/auth/GoogleButton";

export default function SignupPage() {
  const [username, setUsername] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [website, setWebsite] = useState(""); // Honeypot
  const [turnstileToken, setTurnstileToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);
  const [resendIn, setResendIn] = useState(0);
  const [resendMessage, setResendMessage] = useState<string | null>(null);

  const errorBoxRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (error && errorBoxRef.current) {
      errorBoxRef.current.focus();
    }
  }, [error]);

  useEffect(() => {
    let timer: NodeJS.Timeout;
    if (resendIn > 0) {
      timer = setInterval(() => {
        setResendIn((prev) => prev - 1);
      }, 1000);
    }
    return () => clearInterval(timer);
  }, [resendIn]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setResendMessage(null);

    // The daemon enforces this (accounts.go); checking here saves a round trip
    // and a rate-limit slot on the most likely mistake.
    if (!/@(gmail|googlemail)\.com$/i.test(email.trim())) {
      setError("sign-up takes Gmail addresses only (…@gmail.com)");
      return;
    }
    if (password !== confirm) {
      setError("passwords do not match");
      return;
    }
    if (password.length < 8 || password.length > 72) {
      setError("password must be 8-72 characters");
      return;
    }

    setBusy(true);
    try {
      await api.signup(
        username.trim(),
        email.trim(),
        password,
        turnstileToken,
        website,
      );
      setDone(true);
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

  const handleResend = async () => {
    setResendMessage(null);
    setResendIn(30); // Start countdown immediately
    try {
      await api.resendVerify(email.trim());
      setResendMessage("Sent again - check your spam folder too.");
    } catch (err) {
      if (err instanceof ApiError) {
        setResendMessage(err.message);
      } else {
        setResendMessage("Failed to resend verification email.");
      }
    }
  };

  return (
    <div className="flex min-h-[70vh] flex-col items-center justify-center gap-4 px-4">
      <form className="panel w-full max-w-sm" onSubmit={handleSubmit}>
        <div className="panel-h">
          <span
            aria-hidden="true"
            className="inline-block h-2 w-2 shrink-0 rounded-full"
            style={{ background: "var(--accent)" }}
          />
          <span className="mono tracking-[0.22em]">SIGNALDECK</span>
        </div>
        <div className="p-6">
          {done ? (
            <>
              <h1 className="mb-6 text-lg font-bold tracking-widest text-[var(--text)]">
                CHECK YOUR EMAIL
              </h1>
              <div
                role="status"
                className="mb-4 rounded-lg border border-[var(--bid)] bg-[var(--bid-dim)] px-3 py-2 text-xs leading-relaxed text-[var(--bid)]"
              >
                We sent a confirmation link to {email}. Click it to activate
                your account. The link expires in 24 hours.
              </div>
              {resendMessage && (
                <div
                  role="status"
                  className="mb-4 rounded-lg border border-[var(--bid)] bg-[var(--bid-dim)] px-3 py-2 text-xs leading-relaxed text-[var(--bid)]"
                >
                  {resendMessage}
                </div>
              )}
              <button
                type="button"
                onClick={handleResend}
                disabled={resendIn > 0}
                className="text-xs text-[var(--dim)] transition-colors duration-150 hover:text-[var(--accent)]"
              >
                Didn&apos;t get it? Resend
                {resendIn > 0 && ` (${resendIn}s)`}
              </button>
            </>
          ) : (
            <>
              <h1 className="mb-6 text-lg font-bold tracking-widest text-[var(--text)]">
                CREATE ACCOUNT
              </h1>

              {error && (
                <div
                  ref={errorBoxRef}
                  role="alert"
                  tabIndex={-1}
                  className="mb-4 rounded-lg border border-[var(--ask)] bg-[var(--ask-dim)] px-3 py-2 text-xs leading-relaxed text-[var(--ask)]"
                >
                  {error}
                </div>
              )}

              <GoogleButton text="signup_with" onError={setError} />

              <label
                htmlFor="signup-username"
                className="mb-1 block text-xs tracking-wider text-[var(--dim)]"
              >
                USERNAME
              </label>
              <input
                id="signup-username"
                type="text"
                autoComplete="username"
                className="mono mb-1 w-full rounded-lg border border-[var(--border)] bg-[var(--panel2)] px-3 py-2 text-sm text-[var(--text)] outline-none transition-colors duration-150 focus:border-[var(--accent)]"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                required
                minLength={3}
                maxLength={32}
                pattern="^[a-zA-Z0-9._\-]+$"
              />
              <p className="-mt-3 mb-4 text-[10px] text-[var(--faint)]">
                3-32 letters, digits, . _ -
              </p>

              <label
                htmlFor="signup-email"
                className="mb-1 block text-xs tracking-wider text-[var(--dim)]"
              >
                GMAIL ADDRESS
              </label>
              <input
                id="signup-email"
                type="email"
                autoComplete="email"
                placeholder="you@gmail.com"
                className="mono mb-1 w-full rounded-lg border border-[var(--border)] bg-[var(--panel2)] px-3 py-2 text-sm text-[var(--text)] outline-none transition-colors duration-150 focus:border-[var(--accent)]"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                required
              />
              <p className="mb-4 text-[10px] text-[var(--faint)]">
                Gmail addresses only
              </p>

              <label
                htmlFor="signup-password"
                className="mb-1 block text-xs tracking-wider text-[var(--dim)]"
              >
                PASSWORD
              </label>
              <input
                id="signup-password"
                type="password"
                autoComplete="new-password"
                className="mono mb-4 w-full rounded-lg border border-[var(--border)] bg-[var(--panel2)] px-3 py-2 text-sm text-[var(--text)] outline-none transition-colors duration-150 focus:border-[var(--accent)]"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
                minLength={8}
                maxLength={72}
              />

              <label
                htmlFor="signup-confirm-password"
                className="mb-1 block text-xs tracking-wider text-[var(--dim)]"
              >
                CONFIRM PASSWORD
              </label>
              <input
                id="signup-confirm-password"
                type="password"
                autoComplete="new-password"
                className="mono mb-4 w-full rounded-lg border border-[var(--border)] bg-[var(--panel2)] px-3 py-2 text-sm text-[var(--text)] outline-none transition-colors duration-150 focus:border-[var(--accent)]"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                required
                minLength={8}
                maxLength={72}
              />

              {/* Honeypot field */}
              <div
                style={{
                  position: "absolute",
                  left: "-10000px",
                  width: 1,
                  height: 1,
                  overflow: "hidden",
                }}
                aria-hidden="true"
              >
                <label htmlFor="signup-website">Website</label>
                <input
                  id="signup-website"
                  name="website"
                  tabIndex={-1}
                  autoComplete="off"
                  value={website}
                  onChange={(e) => setWebsite(e.target.value)}
                />
              </div>

              <div className="mb-4">
                <Turnstile onToken={setTurnstileToken} />
              </div>

              <button
                type="submit"
                className="w-full cursor-pointer rounded-lg border border-[var(--accent)] bg-transparent px-3 py-2 text-sm font-bold tracking-widest text-[var(--accent)] transition-colors duration-150 hover:bg-[var(--accent)] hover:text-[var(--bg)] disabled:cursor-not-allowed disabled:opacity-40"
                disabled={busy || !turnstileToken}
              >
                {busy ? "CREATING…" : "CREATE ACCOUNT"}
              </button>
            </>
          )}
        </div>
      </form>

      {!done && (
        <Link
          href="/login"
          className="text-xs text-[var(--dim)] transition-colors duration-150 hover:text-[var(--accent)]"
        >
          Already have an account? Sign in
        </Link>
      )}

      <p className="mt-3 text-center text-[10px] text-[var(--faint)]">
        Descriptive market analysis, not financial advice.
      </p>
    </div>
  );
}