"use client";
import { useEffect, useState } from "react";
import { api, ApiError, type AlertPrefs, type TelegramLink } from "@/lib/api";
import Withheld from "@/components/Withheld";
import { WITHHELD_NOTICES } from "@/lib/withheld";

function errText(e: unknown): string {
  if (e instanceof ApiError) {
    switch (e.status) {
      case 401: return "Sign in again to manage alerts.";
      case 403: return "Alerts are not available on this account.";
      case 404: return "Alerts are not available on this deployment yet.";
      default: return e.message;
    }
  }
  return e instanceof Error ? e.message : String(e);
}

export default function AlertsPanel() {
  const [prefs, setPrefs] = useState<AlertPrefs | null>(null);
  const [loadErr, setLoadErr] = useState<string | null>(null);
  const [busy, setBusy] = useState<"email" | "tg-link" | "tg-unlink" | null>(null);
  const [actionErr, setActionErr] = useState<string | null>(null);
  const [link, setLink] = useState<TelegramLink | null>(null);

  const refresh = async () => {
    setPrefs(await api.alertPrefs());
  };

  useEffect(() => {
    let alive = true;
    const load = async () => {
      try {
        const data = await api.alertPrefs();
        if (alive) setPrefs(data);
      } catch (e) {
        if (alive) setLoadErr(errText(e));
      }
    };
    void load();
    return () => {
      alive = false;
    };
  }, []);

  const toggleEmail = async (on: boolean) => {
    setBusy("email");
    setActionErr(null);
    try {
      await api.setEmailDigest(on);
      await refresh();
    } catch (e) {
      setActionErr(errText(e));
    } finally {
      setBusy(null);
    }
  };

  const connect = async () => {
    setBusy("tg-link");
    setActionErr(null);
    try {
      const tgLink = await api.telegramLink();
      setLink(tgLink);
    } catch (e) {
      setActionErr(errText(e));
    } finally {
      setBusy(null);
    }
  };

  const unlink = async () => {
    setBusy("tg-unlink");
    setActionErr(null);
    try {
      await api.telegramUnlink();
      setLink(null);
      await refresh();
    } catch (e) {
      setActionErr(errText(e));
    } finally {
      setBusy(null);
    }
  };

  const confirmLink = async () => {
    try {
      await refresh();
    } catch (e) {
      setActionErr(errText(e));
    }
  };

  return (
    <AlertsBody
      prefs={prefs}
      loadErr={loadErr}
      busy={busy}
      link={link}
      actionErr={actionErr}
      onEmail={(on) => void toggleEmail(on)}
      onConnect={() => void connect()}
      onUnlink={() => void unlink()}
      onConfirmLink={() => void confirmLink()}
    />
  );
}

type AlertsBodyProps = {
  prefs: AlertPrefs | null;
  loadErr: string | null;
  busy: string | null;
  link: TelegramLink | null;
  actionErr: string | null;
  onEmail: (on: boolean) => void;
  onConnect: () => void;
  onUnlink: () => void;
  onConfirmLink: () => void;
};

/** The panel without its state, so a render test can drive every branch. */
export function AlertsBody({ prefs, loadErr, busy, link, actionErr, onEmail, onConnect, onUnlink, onConfirmLink }: AlertsBodyProps) {
  // Both delivery prerequisites must hold before the digest can be switched on.
  const disabledReason = !prefs
    ? null
    : !prefs.mailAvailable
      ? "Email delivery is not set up on this deployment yet."
      : !prefs.emailVerified
        ? "Verify your email address to turn this on."
        : null;

  return (
    <section className="panel w-full max-w-md" aria-labelledby="alerts-h">
      <div className="panel-h">
        <span id="alerts-h" className="mono tracking-[0.22em]">ALERTS</span>
      </div>
      <div className="flex flex-col gap-4 p-6 text-sm">
        {prefs === null ? loadErr ? (
          <p role="alert" className="text-[var(--ask)]">{loadErr}</p>
        ) : (
          <p className="text-[var(--dim)]">Loading alert settings…</p>
        ) : prefs.digestWithheld ? (
          // The daemon sends the digest to the operator only (memberdigest),
          // so a switch here would confirm a delivery that never comes. Only
          // turning things OFF stays available.
          <>
            <Withheld
              compact
              title="Daily digest"
              notice={prefs.digestNotice ?? WITHHELD_NOTICES.alpaca}
              source="alpaca"
            />
            {prefs.emailDigest && (
              <button type="button" disabled={busy !== null} className="w-full cursor-pointer rounded-lg border border-[var(--border)] bg-transparent px-3 py-2 text-sm font-bold tracking-widest text-[var(--dim)] transition-colors duration-150 hover:border-[var(--accent)] hover:text-[var(--accent)] disabled:cursor-not-allowed disabled:opacity-50" onClick={() => onEmail(false)}>
                Turn the email digest off
              </button>
            )}
            {prefs.telegramLinked && (
              <button type="button" disabled={busy !== null} className="w-full cursor-pointer rounded-lg border border-[var(--border)] bg-transparent px-3 py-2 text-sm font-bold tracking-widest text-[var(--dim)] transition-colors duration-150 hover:border-[var(--accent)] hover:text-[var(--accent)] disabled:cursor-not-allowed disabled:opacity-50" onClick={onUnlink}>
                Disconnect Telegram
              </button>
            )}
            {actionErr && (
              <p role="alert" className="m-0 text-xs text-[var(--ask)]">{actionErr}</p>
            )}
          </>
        ) : (
          <>
            <div className="flex flex-col gap-2">
              <h2 className="m-0 text-sm font-bold tracking-widest text-[var(--text)]">
                DAILY EMAIL DIGEST
              </h2>
              <p className="m-0 leading-relaxed text-[var(--dim)]">
                One email each trading morning with SignalDeck&rsquo;s current reads for your watchlist and any that changed since yesterday. No prices. At most one a day. Unsubscribe from any email.
              </p>
              <label className="flex min-h-[40px] cursor-pointer items-center gap-2 text-[var(--text)]">
                <input
                  type="checkbox"
                  checked={prefs.emailDigest}
                  // Turning it OFF is always allowed, even if a prerequisite later lapsed.
                  disabled={(disabledReason !== null && !prefs.emailDigest) || busy !== null}
                  onChange={(e) => onEmail(e.target.checked)}
                  aria-describedby={disabledReason ? "digest-why" : undefined}
                  className="h-4 w-4 cursor-pointer accent-[var(--accent)]"
                />
                Send me the daily digest
              </label>
              {disabledReason && (
                <p id="digest-why" className="m-0 text-xs text-[var(--faint)]">
                  {disabledReason}
                </p>
              )}
            </div>
            {prefs.telegramAvailable && (
              <div className="flex flex-col gap-2">
                <h2 className="m-0 text-sm font-bold tracking-widest text-[var(--text)]">
                  TELEGRAM
                </h2>
                {prefs.telegramLinked ? (
                  <>
                    <p className="m-0 text-[var(--dim)]">
                      Telegram is connected. The same daily read arrives there.
                    </p>
                    <button
                      type="button"
                      disabled={busy !== null}
                      className="w-full cursor-pointer rounded-lg border border-[var(--border)] bg-transparent px-3 py-2 text-sm font-bold tracking-widest text-[var(--dim)] transition-colors duration-150 hover:border-[var(--accent)] hover:text-[var(--accent)] disabled:cursor-not-allowed disabled:opacity-50"
                      onClick={onUnlink}
                    >
                      Disconnect Telegram
                    </button>
                  </>
                ) : link ? (
                  <>
                    <p className="m-0 text-[var(--dim)]">
                      Send this code to the SignalDeck bot
                      {link.botUsername ? (
                        <>
                          {" "}(
                          <a href={`https://t.me/${link.botUsername}`} target="_blank" rel="noopener noreferrer" className="underline">
                            @{link.botUsername}
                          </a>
                          )
                        </>
                      ) : null}{" "}
                      within 15 minutes:
                    </p>
                    <p
                      className="mono m-0 select-all text-lg font-bold tracking-[0.2em] text-[var(--text)]"
                      aria-label="Telegram link code"
                    >
                      {link.code}
                    </p>
                    <button
                      type="button"
                      disabled={busy !== null}
                      className="w-full cursor-pointer rounded-lg border border-[var(--border)] bg-transparent px-3 py-2 text-sm font-bold tracking-widest text-[var(--dim)] transition-colors duration-150 hover:border-[var(--accent)] hover:text-[var(--accent)] disabled:cursor-not-allowed disabled:opacity-50"
                      onClick={onConfirmLink}
                    >
                      I&rsquo;ve sent it
                    </button>
                    <p className="m-0 text-xs text-[var(--faint)]">
                      Expires at {new Date(link.expiresAt * 1000).toLocaleTimeString()}
                    </p>
                  </>
                ) : (
                  <>
                    <p className="m-0 text-[var(--dim)]">
                      Get the same daily read as a Telegram message.
                    </p>
                    <button
                      type="button"
                      disabled={busy !== null}
                      className="w-full cursor-pointer rounded-lg border border-[var(--border)] bg-transparent px-3 py-2 text-sm font-bold tracking-widest text-[var(--dim)] transition-colors duration-150 hover:border-[var(--accent)] hover:text-[var(--accent)] disabled:cursor-not-allowed disabled:opacity-50"
                      onClick={onConnect}
                    >
                      Connect Telegram
                    </button>
                  </>
                )}
              </div>
            )}
            {actionErr && (
              <p role="alert" className="m-0 text-xs text-[var(--ask)]">{actionErr}</p>
            )}
          </>
        )}
      </div>
    </section>
  );
}
