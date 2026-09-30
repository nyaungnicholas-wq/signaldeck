"use client";
import { useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { api } from "@/lib/api";

type User = {
  id: number;
  username: string;
  isAdmin: boolean;
};

export default function AccountPage() {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const router = useRouter();

  useEffect(() => {
    api.me()
      .then(data => {
        setUser(data);
        setLoading(false);
      })
      .catch(() => {
        router.replace("/login");
        setLoading(false);
      });
  }, []);

  if (loading) {
    return (
      <div className="flex min-h-[70vh] flex-col items-center justify-center gap-4 px-4">
        <p className="text-sm text-[var(--dim)]">Loading…</p>
      </div>
    );
  }

  if (!user) {
    return null;
  }

  const handleLogout = async () => {
    await api.logout();
    router.replace("/");
  };

  return (
    <div className="flex min-h-[70vh] flex-col items-center justify-center gap-4 px-4">
      <div className="panel w-full max-w-md">
        <div className="panel-h">
          <span aria-hidden="true" className="inline-block h-2 w-2 shrink-0 rounded-full" style={{ background: "var(--accent)" }} />
          <span className="mono tracking-[0.22em]">SIGNALDECK</span>
        </div>
        <div className="p-6">
          <h1 className="mb-4 text-lg font-bold tracking-widest text-[var(--text)]">YOUR ACCOUNT</h1>
          <p className="mb-4">
            Signed in as <strong className="text-[var(--text)]">{user.username}</strong>
          </p>
          <p className="text-sm leading-relaxed text-[var(--dim)] mb-6">
            Your account is active. Member accounts see the public record — the live grades, the volatility record and the hash-chained receipts — and get updates as new evidence lands. Descriptive market analysis, not financial advice.
          </p>
          <div className="space-y-2">
            <Link href="/" className="text-sm text-[var(--dim)] transition-colors duration-150 hover:text-[var(--accent)]">
              Home
            </Link>
            <Link href="/accuracy" className="text-sm text-[var(--dim)] transition-colors duration-150 hover:text-[var(--accent)]">
              Live grades
            </Link>
            <Link href="/volatility" className="text-sm text-[var(--dim)] transition-colors duration-150 hover:text-[var(--accent)]">
              Volatility record
            </Link>
            <Link href="/proof" className="text-sm text-[var(--dim)] transition-colors duration-150 hover:text-[var(--accent)]">
              Receipts
            </Link>
            {user.isAdmin && (
              <Link href="/dashboard" className="text-sm text-[var(--dim)] transition-colors duration-150 hover:text-[var(--accent)]">
                Operator dashboard
              </Link>
            )}
          </div>
          <button onClick={handleLogout} className="mt-6 w-full cursor-pointer rounded-lg border border-[var(--border)] bg-transparent px-3 py-2 text-sm font-bold tracking-widest text-[var(--dim)] transition-colors duration-150 hover:border-[var(--accent)] hover:text-[var(--accent)]">
            SIGN OUT
          </button>
        </div>
      </div>
    </div>
  );
}