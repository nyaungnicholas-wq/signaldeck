// Plain-English summary of a grader refusal reason.
//
// Lives here rather than inside RefusalNotice.tsx so it can be exercised by
// node --test, which strips TypeScript types but does not compile JSX.

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
		// Only the dates of the collapsed day-horizons ("1d 2026-07-27", "1w 2026-08-04"),
		// never the refused-since timestamp that can precede the list.
		// The boundary must be the regex escape \b. This literal was once a raw
		// U+0008 backspace, so the pattern demanded a control character before
		// "1d", matched nothing, and the date range silently vanished.
		const dateMatches = [...raw.matchAll(/\b1[dwh] (\d{4}-\d{2}-\d{2})/g)].map((m) => m[1]);
		let betweenClause = "";
		if (dateMatches.length) {
			const sorted = [...dateMatches].sort();
			const earliest = sorted[0];
			const latest = sorted[sorted.length - 1];
			betweenClause = ` between ${earliest} and ${latest}`;
		}
		// The window is fixed at the survivorship epoch and does not roll forward,
		// so the old "withheld until the window clears" wording promised something
		// waiting cannot deliver. Say what the grader's own reason says.
		const headline = `${n} of the ${m} graded day-horizons in the window had a collapsed cross-section${betweenClause}: the model handed the whole universe a handful of distinct probabilities, so those rows are one market-wide call repeated per symbol, not independent forecasts. This window does not roll forward, so those days stay in it and the figures stay withheld — waiting does not clear this.`;
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
