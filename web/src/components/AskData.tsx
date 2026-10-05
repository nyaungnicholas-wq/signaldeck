"use client";

import React, { useEffect, useState, useMemo } from "react";
import { api, ApiError } from "@/lib/api";
import {
  ASK_FOOTER,
  splitCitations,
  formatCell,
  type AskStatus,
  type AskAnswer,
  type AskCitation,
} from "@/lib/ask";

// Reusable component for rendering a row's key-value pairs
function RowTable({ row }: { row: Record<string, unknown> }) {
  return (
    <dl className="grid grid-cols-[auto_1fr] gap-x-2 gap-y-1 text-[0.8rem]">
      {Object.entries(row).map(([key, value]) => (
        <React.Fragment key={key}>
          <dt className="mono font-medium" style={{ color: "var(--dim)" }}>
            {key}:
          </dt>
          <dd className="m-0">{formatCell(key, value)}</dd>
        </React.Fragment>
      ))}
    </dl>
  );
}

export default function AskData() {
  const [status, setStatus] = useState<AskStatus | null>(null);
  const [statusError, setStatusError] = useState<string | null>(null);
  const [question, setQuestion] = useState("");
  const [isBusy, setIsBusy] = useState(false);
  const [answer, setAnswer] = useState<AskAnswer | null>(null);
  const [askError, setAskError] = useState<string | null>(null);
  const [openId, setOpenId] = useState<string | null>(null);

  // Fetch status on mount
  useEffect(() => {
    let alive = true;
    const fetchStatus = async () => {
      try {
        const s = await api.askStatus();
        if (alive) {
          setStatus(s);
          setStatusError(null);
        }
      } catch (e) {
        if (alive) {
          setStatusError(
            e instanceof ApiError ? e.message : String(e)
          );
        }
      }
    };
    void fetchStatus();
    return () => {
      alive = false;
    };
  }, []);

  const trimmedQuestion = question.trim();
  const maxQuestionLength = status?.maxQuestion ?? 500;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!trimmedQuestion || isBusy) return;

    setIsBusy(true);
    setAskError(null);
    setAnswer(null); // Clear previous answer
    setOpenId(null); // Clear open citation

    try {
      const res = await api.ask(trimmedQuestion);
      setAnswer(res);
    } catch (e) {
      setAskError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setIsBusy(false);
    }
  };

  const citationMap = useMemo(() => {
    const map = new Map<string, AskCitation>();
    if (answer?.citations) {
      for (const citation of answer.citations) {
        map.set(citation.id, citation);
      }
    }
    return map;
  }, [answer]);

  const handleCitationClick = (id: string) => {
    setOpenId((prevId) => (prevId === id ? null : id));
  };

  return (
    <section className="panel flex flex-col gap-4 px-4 py-4" aria-labelledby="ask-h">
      <h1 id="ask-h" className="m-0 text-lg font-bold">
        Ask the data
      </h1>
      <p className="m-0 text-[0.85rem]" style={{ color: "var(--dim)" }}>
        Ask a question about SignalDeck&rsquo;s own forecasts and record. Every answer cites the rows it came from.
      </p>

      {statusError ? (
        <p role="alert" className="m-0 text-[0.85rem]" style={{ color: "var(--ask)" }}>
          Error loading status: {statusError}
        </p>
      ) : status === null ? (
        <p className="m-0 text-[0.85rem]" style={{ color: "var(--dim)" }}>
          Loading...
        </p>
      ) : !status.available ? (
        <p className="m-0 text-[0.85rem]" style={{ color: "var(--dim)" }}>
          {status.reason}
        </p>
      ) : (
        <>
          <form onSubmit={handleSubmit} className="flex flex-col gap-3">
            <label htmlFor="ask-question" className="text-[0.75rem] tracking-wide" style={{ color: "var(--dim)" }}>
              Your question
            </label>
            <textarea
              id="ask-question"
              rows={3}
              maxLength={maxQuestionLength}
              value={question}
              onChange={(e) => setQuestion(e.target.value)}
              className="chip w-full max-w-[40rem] px-3 py-2"
              style={{ color: "var(--text)" }}
            />
            <div className="flex items-center justify-between">
              <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                {question.length}/{maxQuestionLength}
              </span>
              <button
                type="submit"
                disabled={isBusy || !trimmedQuestion}
                className="chip min-h-[40px] cursor-pointer px-3"
                style={{ color: "var(--accent)" }}
              >
                {isBusy ? "Asking..." : "Ask"}
              </button>
            </div>
            {status.dailyLimit && (
              <p className="m-0 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                {status.dailyLimit} questions a day.
              </p>
            )}
          </form>

          {askError && (
            <p role="alert" className="m-0 text-[0.85rem]" style={{ color: "var(--ask)" }}>
              Error: {askError}
            </p>
          )}

          {answer && (
            <div className="flex flex-col gap-3">
              {answer.fallback ? (
                <p role="status" className="m-0 text-[0.85rem]" style={{ color: "var(--dim)" }}>
                  {answer.answer}
                </p>
              ) : (
                <p className="m-0 text-[0.9rem] leading-relaxed">
                  {splitCitations(answer.answer).map((part, i) =>
                    "text" in part ? (
                      <React.Fragment key={i}>{part.text}</React.Fragment>
                    ) : (
                      <React.Fragment key={i}>
                      {part.ids.map((id, j) => (
                        <button
                          key={`${i}-${j}-${id}`}
                          type="button"
                          onClick={() => handleCitationClick(id)}
                          aria-expanded={openId === id}
                          aria-controls={`ask-row-${id.replace(/:/g, "-")}`}
                          className="chip mono mx-0.5 cursor-pointer px-1.5 text-[0.75rem]"
                          style={{ color: "var(--accent)" }}
                        >
                          {id}
                        </button>
                      ))}
                      </React.Fragment>
                    )
                  )}
                </p>
              )}

              {openId && citationMap.has(openId) && (
                <div
                  id={`ask-row-${openId.replace(/:/g, "-")}`}
                  className="panel flex flex-col gap-2 px-3 py-2"
                  style={{ borderColor: "var(--border)" }}
                >
                  <p className="m-0 text-[0.85rem]">
                    <span className="mono font-bold">{citationMap.get(openId)?.query}</span>
                  </p>
                  <RowTable row={citationMap.get(openId)?.row ?? {}} />
                </div>
              )}

              <h2 className="m-0 text-base font-bold">
                {answer.fallback ? "Rows returned" : "Rows cited"}
              </h2>
              <ul className="m-0 flex list-none flex-col gap-2 p-0">
                {answer.citations.map((c) => (
                  <li key={c.id}>
                    <details className="border-b pb-2" style={{ borderColor: "var(--border)" }}>
                      <summary className="cursor-pointer text-[0.85rem] font-medium">
                        <span className="mono">{c.id}</span> · {c.query}
                      </summary>
                      <div className="pt-2 pl-4">
                        <RowTable row={c.row} />
                      </div>
                    </details>
                  </li>
                ))}
              </ul>

              <h2 className="m-0 text-base font-bold">Queries run</h2>
              <ul className="m-0 flex list-none flex-col gap-1 p-0 text-[0.8rem]">
                {answer.queries.map((q, i) => (
                  <li key={i}>
                    <span className="mono">{q.name}</span>{" "}
                    <span style={{ color: "var(--faint)" }}>
                      {JSON.stringify(q.params)}
                    </span>
                    {q.truncated && (
                      <span className="chip ml-2 px-1.5 text-[0.7rem]" style={{ color: "var(--dim)" }}>
                        truncated: more rows matched than shown
                      </span>
                    )}
                  </li>
                ))}
              </ul>
              <p className="m-0 text-[0.75rem]" style={{ color: "var(--dim)" }}>
                {answer.model} · {answer.tookMs} ms
              </p>
            </div>
          )}
        </>
      )}

      <p className="m-0 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
        {ASK_FOOTER}
      </p>
    </section>
  );
}