import type { ReactNode } from "react";

export function summarizeRefusal(reason: string | null | undefined): { headline: string; detail: string } {
	const raw = reason ?? "";
	const detail = raw;
	if (!raw.trim()) return { headline: "Publication refused.", detail: "" };

	// 1. collapsed cross-section pattern
	const collapsedMatch = raw.match(/(\d+) collapsed cross-section\(s\) of (\d+) day\(s\):/);
	if (collapsedMatch) {
		const [, nStr, mStr] = collapsedMatch;
		const n = parseInt(nStr, 10);
		const m = parseInt(mStr, 10);
		const dateMatches = raw.match(/\d{4}-\d{2}-\d{2}/g) ?? [];
		let betweenClause = "";
		if (dateMatches.length) {
			const sorted = [...dateMatches].sort();
			const earliest = sorted[0];
			const latest = sorted[sorted.length - 1];
			betweenClause = ` between ${earliest} and ${latest}`;
		}
		const headline = `${n} of the ${m} graded day-horizons in the window had a collapsed cross-section${betweenClause}: the model handed the whole universe a handful of distinct probabilities, so those rows are one market-wide call repeated per symbol, not independent forecasts. Figures are withheld until the window clears.`;
		return { headline, detail };
	}

	// 2. grader has been refusing since pattern
	const refusingMatch = raw.match(/grader has been refusing since (\S+?):?(\s|$)/);
	if (refusingMatch) {
		const ts = refusingMatch[1];
		let extra = "";
		const colonIdx = raw.indexOf(": ", refusingMatch.index);
		if (colonIdx !== -1) {
			const after = raw.slice(colonIdx + 2).trim();
			if (after) {
				const trimmed = after.length > 240 ? after.slice(0, 240) + "…" : after;
				extra = ` ${trimmed}`;
			}
		}
		const headline = `The grader has been refusing to publish since ${ts}.${extra}`;
		return { headline, detail };
	}

	// 3. last successful grade was ... ago
	const gradeMatch = raw.match(/last successful grade was (.+?) ago/);
	if (gradeMatch) {
		const age = gradeMatch[1];
		const headline = `The last successful grade is ${age} old, past the freshness limit, so nothing here is current.`;
		return { headline, detail };
	}

	// 4. registry unavailable etc.
	if (/registry unavailable|unreachable|not readable|could not be read/i.test(raw)) {
		return { headline: "The grading service could not be read, so no figures are shown.", detail };
	}

	// fallback
	const fallback = raw.length > 240 ? raw.slice(0, 240) + "…" : raw;
	const headline = fallback || "Publication refused.";
	return { headline, detail };
}

export default function RefusalNotice(props: {
	status?: string;
	reason?: string | null;
	gradedAt?: string | null;
	refusedSince?: string | null;
	title?: string;
	tone?: "bad" | "warn";
	compact?: boolean;
	testId?: string;
	children?: ReactNode;
}) {
	const { status, reason, gradedAt, refusedSince, title, tone = "bad", compact, testId, children } = props;
	const { headline, detail } = summarizeRefusal(reason);
	const baseCls = `flex flex-col gap-2 rounded-lg border ${compact ? "px-3 py-2" : "px-4 py-3"}`;
	const toneVar = tone === "warn" ? "--warn" : "--bad";
	const style = {
		borderColor: `color-mix(in srgb, var(${toneVar}) 55%, transparent)`,
		backgroundColor: `color-mix(in srgb, var(${toneVar}) 10%, transparent)`,
		color: "var(--text)",
	};
	return (
		<div
			role="status"
			data-testid={testId ?? "refusal-notice"}
			data-status={status ?? "REFUSED"}
			className={baseCls}
			style={style}
		>
			<div
				className="mono text-[0.7rem] uppercase tracking-[0.15em]"
				style={{ color: tone === "warn" ? "var(--warn)" : "var(--bad)" }}
			>
				{title ?? "Publication refused"}{status ? ` · ${status}` : ""}
			</div>
			<p className={compact ? "m-0 max-w-[70ch] text-[0.8rem] leading-relaxed" : "m-0 max-w-[70ch] text-[0.9rem] leading-relaxed"}>
				{headline}
			</p>
			{(gradedAt || refusedSince) && (
				<div className="text-[0.75rem]" style={{ color: "var(--dim)" }}>
					{[refusedSince && `Refused since ${refusedSince}`, gradedAt && `Withheld grade computed at ${gradedAt}`]
						.filter(Boolean)
						.join(" · ")}
				</div>
			)}
			{detail && detail !== headline && (
				<details className="text-[0.75rem]">
					<summary className="cursor-pointer" style={{ color: "var(--dim)" }}>
						Full reason from the grader
					</summary>
					<p className="mono mt-2 max-w-[80ch] whitespace-pre-wrap break-words text-[0.72rem] leading-relaxed" style={{ color: "var(--dim)" }}>
						{detail}
					</p>
				</details>
			)}
			{children}
		</div>
	);
}