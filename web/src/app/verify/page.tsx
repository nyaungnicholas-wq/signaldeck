"use client";
import { useEffect, useState, useRef, Suspense } from "react";
import { useSearchParams, useRouter } from "next/navigation";
import Link from "next/link";
import { api, ApiError } from "@/lib/api";

function VerifyInner() {
  const searchParams = useSearchParams();
  const router = useRouter();
  const token = searchParams.get("token") ?? "";
  const [status, setStatus] = useState<"working" | "ok" | "error">(token ? "working" : "error");
  const [message, setMessage] = useState(token ? "" : "This link is missing its token.");
  const executedRef = useRef(false);

  useEffect(() => {
    if (executedRef.current) return;
    executedRef.current = true;

    if (!token) return;

    // A 503 means the server was busy and the link was NOT spent, so try again
    // quietly a few times before showing anything.
    const attempt = async (left: number): Promise<Awaited<ReturnType<typeof api.verifyEmail>>> => {
      try {
        return await api.verifyEmail(token);
      } catch (err) {
        if (left > 0 && err instanceof ApiError && err.status === 503) {
          await new Promise((r) => setTimeout(r, 2500));
          return attempt(left - 1);
        }
        throw err;
      }
    };
    attempt(4)
      .then((data) => {
        setStatus("ok");
        setTimeout(() => {
          router.replace(data.isAdmin ? "/dashboard" : "/account");
        }, 1500);
      })
      .catch((err) => {
        if (err instanceof ApiError) {
          setStatus("error");
          // A link opened twice (two tabs, a second click) is spent by the
          // first — and that first one confirmed the account.
          setMessage(err.status === 400
            ? "This link was already used or has expired. If you clicked it before, your email is confirmed. Just sign in."
            : err.message);
        } else {
          setStatus("error");
          setMessage("Request failed");
        }
      });
  }, [token, router]);

  return (
    <div className="flex min-h-[70vh] flex-col items-center justify-center gap-4 px-4">
      <form className="panel w-full max-w-sm">
        <div className="panel-h">
          <span aria-hidden="true" className="inline-block h-2 w-2 shrink-0 rounded-full" style={{ background: "var(--accent)" }} />
          <span className="mono tracking-[0.22em]">SIGNALDECK</span>
        </div>
        <div className="p-6">
          <h1 className="mb-6 text-lg font-bold tracking-widest text-[var(--text)]">CONFIRM EMAIL</h1>
          {status === "working" && (
            <p className="text-sm text-[var(--dim)]">Confirming…</p>
          )}
          {status === "ok" && (
            <div className="mb-4 rounded-lg border border-[var(--bid)] bg-[var(--bid-dim)] px-3 py-2 text-xs leading-relaxed text-[var(--bid)]" role="status">
              Email confirmed. Signing you in…
            </div>
          )}
          {status === "error" && (
            <>
              <div className="mb-4 rounded-lg border border-[var(--ask)] bg-[var(--ask-dim)] px-3 py-2 text-xs leading-relaxed text-[var(--ask)]" role="alert">
                {message}
              </div>
              <div className="flex flex-col items-center gap-2">
                <Link href="/signup" className="text-xs text-[var(--dim)] transition-colors duration-150 hover:text-[var(--accent)]">
                  Create a new account
                </Link>
                <span className="text-xs text-[var(--dim)]">·</span>
                <Link href="/login" className="text-xs text-[var(--dim)] transition-colors duration-150 hover:text-[var(--accent)]">
                  Sign in
                </Link>
              </div>
            </>
          )}
        </div>
      </form>
    </div>
  );
}

export default function VerifyPage() {
  return (
    <Suspense fallback={null}>
      <VerifyInner />
    </Suspense>
  );
}