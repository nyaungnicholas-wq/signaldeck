"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import PagePurpose from "@/components/PagePurpose";
import { api } from "@/lib/api";

/** Minimal login / register form in the SignalDeck terminal style. */
export default function LoginPage() {
  const router = useRouter();
  const [mode, setMode] = useState<"login" | "register">("login");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const errorRef = useRef<HTMLDivElement | null>(null);
  // Whether this deployment accepts new accounts. The page used to offer
  // "no account? register →" unconditionally, directly under copy calling
  // SignalDeck "a private workspace" — so on a closed deployment it advertised
  // a door that answers 403, and on an open one it advertised a real door to
  // anyone who reached the tunnel. Starts null (unknown) so nothing flashes in
  // and then disappears.
  const [openSignup, setOpenSignup] = useState<boolean | null>(null);

  useEffect(() => {
    let live = true;
    fetch("/api/health")
      .then((r) => (r.ok ? r.json() : null))
      .then((h) => {
        if (live) setOpenSignup(h?.openSignup === true);
      })
      // Health unreachable: assume closed. Offering registration we cannot
      // confirm is the worse of the two guesses.
      .catch(() => live && setOpenSignup(false));
    return () => {
      live = false;
    };
  }, []);

  // Already signed in? Go where the submit button would have sent you.
  //
  // PublicNav renders its "Sign in" chip on every public route and has no
  // session to check — it cannot know. So a signed-in user who lands on /,
  // /accuracy or /proof sees the anonymous chrome, follows the only affordance
  // it offers, and arrives at a login form that gives no sign they are already
  // in. Checking here rather than in the nav keeps the cost on the ONE page
  // where a session question is already being asked: the anonymous surface is
  // deliberately lean and must not gain a per-visitor /api/auth/me call.
  //
  // A 401 throws, which is the not-signed-in path and correctly leaves the form
  // on screen. replace(), not push(), so Back does not bounce off this page.
  useEffect(() => {
    let live = true;
    api
      .me()
      .then(() => {
        if (live) router.replace("/dashboard");
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [router]);

  // On a failed submit the error box appears; move focus to it so keyboard
  // and screen-reader users land on the message.
  useEffect(() => {
    if (error) errorRef.current?.focus();
  }, [error]);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      if (mode === "login") await api.login(username, password);
      else await api.register(username, password);
      router.replace("/dashboard");
    } catch (err) {
      setError(err instanceof Error ? err.message : "request failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex min-h-[70vh] flex-col items-center justify-center gap-4 px-4">
      <div className="w-full max-w-sm">
        <PagePurpose
          id="login"
          text={
            "SignalDeck is a private workspace — sign in to open your dashboard, signals, and research." +
            (openSignup ? " No account? Create one to get started." : "") +
            " Everything inside is descriptive market analysis, not financial advice."
          }
        />
      </div>
      <form onSubmit={submit} className="panel w-full max-w-sm">
        <div className="panel-h">
          <span
            aria-hidden="true"
            className="inline-block h-2 w-2 shrink-0 rounded-full"
            style={{ background: "var(--accent)" }}
          />
          <span className="mono tracking-[0.22em]">SIGNALDECK</span>
        </div>
        <div className="p-6">
          <h1 className="mb-6 text-lg font-bold tracking-widest text-[var(--text)]">
            {mode === "login" ? "SIGN IN" : "CREATE ACCOUNT"}
          </h1>

          <label
            htmlFor="login-username"
            className="mb-1 block text-xs tracking-wider text-[var(--dim)]"
          >
            USERNAME
          </label>
          <input
            autoFocus
            id="login-username"
            name="username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="username"
            aria-invalid={error ? true : undefined}
            aria-describedby={error ? "login-error" : undefined}
            className="mono mb-4 w-full rounded-lg border border-[var(--border)] bg-[var(--panel2)] px-3 py-2 text-sm text-[var(--text)] outline-none transition-colors duration-150 focus:border-[var(--accent)]"
          />

          <label
            htmlFor="login-password"
            className="mb-1 block text-xs tracking-wider text-[var(--dim)]"
          >
            PASSWORD
          </label>
          <input
            type="password"
            id="login-password"
            name="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete={mode === "login" ? "current-password" : "new-password"}
            aria-invalid={error ? true : undefined}
            aria-describedby={error ? "login-error" : undefined}
            className="mono mb-4 w-full rounded-lg border border-[var(--border)] bg-[var(--panel2)] px-3 py-2 text-sm text-[var(--text)] outline-none transition-colors duration-150 focus:border-[var(--accent)]"
          />

          {error && (
            <div
              id="login-error"
              role="alert"
              tabIndex={-1}
              ref={errorRef}
              className="mb-4 rounded-lg border border-[var(--ask)] bg-[var(--ask-dim)] px-3 py-2 text-xs leading-relaxed text-[var(--ask)]"
            >
              {error}
            </div>
          )}

          <button
            type="submit"
            disabled={busy || !username || !password}
            aria-busy={busy}
            aria-label={
              busy
                ? mode === "login"
                  ? "signing in…"
                  : "creating account…"
                : undefined
            }
            className="w-full cursor-pointer rounded-lg border border-[var(--accent)] bg-transparent px-3 py-2 text-sm font-bold tracking-widest text-[var(--accent)] transition-colors duration-150 hover:bg-[var(--accent)] hover:text-[var(--bg)] disabled:cursor-not-allowed disabled:opacity-40"
          >
            {busy ? "…" : mode === "login" ? "SIGN IN" : "REGISTER"}
          </button>

          {openSignup && (
            <button
              type="button"
              onClick={() => {
                setMode(mode === "login" ? "register" : "login");
                setError(null);
              }}
              className="mt-4 w-full cursor-pointer text-center text-xs text-[var(--dim)] transition-colors duration-150 hover:text-[var(--accent)]"
            >
              {mode === "login"
                ? "no account? register →"
                : "have an account? sign in →"}
            </button>
          )}
        </div>
      </form>
    </div>
  );
}
