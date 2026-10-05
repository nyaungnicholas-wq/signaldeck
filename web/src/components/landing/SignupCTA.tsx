import Link from "next/link";

export default function SignupCTA() {
  return (
    <section className="cta-shell relative overflow-hidden rounded-2xl p-[1px]">
      <span aria-hidden="true" className="cta-orb cta-orb-1" />
      <span aria-hidden="true" className="cta-orb cta-orb-2" />
      <span aria-hidden="true" className="cta-orb cta-orb-3" />
      <div className="cta-inner relative flex flex-col items-start gap-5 rounded-2xl px-6 py-10 sm:px-10 sm:py-14">
        <div className="mono text-[0.7rem] uppercase tracking-[0.22em]" style={{ color: "var(--accent)" }}>
          Free account
        </div>
        <h2 className="cta-title m-0 max-w-[18ch] text-[1.9rem] font-extrabold leading-[1.05] sm:text-[2.6rem]">
          Watch the record get written.
        </h2>
        <p className="max-w-[56ch] text-[0.98rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          Make an account to follow the live grades as the evidence lands — including the ones that fail. We confirm your email, never sell it, and never send anything but the record.
        </p>
        <div className="flex flex-wrap items-center gap-3">
          <Link href="/signup" className="cta-primary">Create free account</Link>
          <Link href="/login" className="cta-secondary">Sign in</Link>
        </div>
        <p className="m-0 text-[0.72rem]" style={{ color: "var(--faint)" }}>
          Descriptive market analysis, not financial advice.
        </p>
      </div>
    </section>
  );
}