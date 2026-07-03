"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { api } from "@/lib/api";

/** Minimal login / register form in the SignalDeck terminal style. */
export default function LoginPage() {
  const router = useRouter();
  const [mode, setMode] = useState<"login" | "register">("login");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      if (mode === "login") await api.login(username, password);
      else await api.register(username, password);
      router.replace("/");
    } catch (err) {
      setError(err instanceof Error ? err.message : "request failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex min-h-[70vh] items-center justify-center px-4">
      <form
        onSubmit={submit}
        className="w-full max-w-sm border border-[var(--border)] bg-[var(--panel)] p-6"
      >
        <div className="mb-1 text-xs tracking-widest text-[var(--faint)]">
          SIGNALDECK
        </div>
        <h1 className="mb-6 text-lg font-bold text-[var(--text)]">
          {mode === "login" ? "SIGN IN" : "CREATE ACCOUNT"}
        </h1>

        <label className="mb-1 block text-xs text-[var(--dim)]">USERNAME</label>
        <input
          autoFocus
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          autoComplete="username"
          className="mb-4 w-full border border-[var(--border)] bg-[var(--panel2)] px-3 py-2 text-sm text-[var(--text)] outline-none focus:border-[var(--accent)]"
        />

        <label className="mb-1 block text-xs text-[var(--dim)]">PASSWORD</label>
        <input
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          autoComplete={mode === "login" ? "current-password" : "new-password"}
          className="mb-4 w-full border border-[var(--border)] bg-[var(--panel2)] px-3 py-2 text-sm text-[var(--text)] outline-none focus:border-[var(--accent)]"
        />

        {error && (
          <div className="mb-4 border border-[var(--ask)] bg-[var(--ask-dim)] px-3 py-2 text-xs text-[var(--ask)]">
            {error}
          </div>
        )}

        <button
          type="submit"
          disabled={busy || !username || !password}
          className="w-full border border-[var(--accent)] bg-transparent px-3 py-2 text-sm font-bold tracking-widest text-[var(--accent)] hover:bg-[var(--accent)] hover:text-[var(--bg)] disabled:opacity-40"
        >
          {busy ? "…" : mode === "login" ? "SIGN IN" : "REGISTER"}
        </button>

        <button
          type="button"
          onClick={() => {
            setMode(mode === "login" ? "register" : "login");
            setError(null);
          }}
          className="mt-4 w-full text-center text-xs text-[var(--dim)] hover:text-[var(--text)]"
        >
          {mode === "login"
            ? "no account? register →"
            : "have an account? sign in →"}
        </button>
      </form>
    </div>
  );
}
