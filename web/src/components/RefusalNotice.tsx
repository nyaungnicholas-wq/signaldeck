import type { ReactNode } from "react";
import { summarizeRefusal } from "@/lib/refusal";

// Re-exported for existing importers; the implementation moved to src/lib so
// node --test can reach it without a JSX compiler.
export { summarizeRefusal };

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
			data-tone={tone}
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