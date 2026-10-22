// Crypto has no licensed data source, so it is paused: nothing can be added,
// subscribed, ordered or forecast. Its history stays on the public record,
// labelled with this one line so every page says the same date.
export const CRYPTO_PAUSED_LABEL = "crypto paused since 22 October 2026";

/** True for a crypto-universe predictor or kind: "trend21-crypto", "liquidity21-crypto#persist". */
export function isCryptoPredictor(name: string | null | undefined): boolean {
  return /-crypto(#|$)/.test(name ?? "");
}
