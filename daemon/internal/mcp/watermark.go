// LAYER 6 — provenance.
//
// Output is watermarked per client so text that later surfaces elsewhere is
// traceable to the client that pulled it. Two rules govern what may be varied:
//
//  1. NEVER a number, a band, a sample size, a verdict or a caveat. A
//     watermark that changes a figure is data corruption wearing a security
//     hat, and it would make two clients' copies of the same verdict disagree.
//  2. Only genuinely insignificant surface: which of several equivalent
//     closing sentences the disclaimer ends with, and the rotation of lists
//     whose order carries no meaning.
//
// The mark is deterministic — the same client always gets the same variant —
// so a client cannot detect it by diffing its own responses, and an operator
// can attribute a leak by recomputing rather than by looking anything up.
//
// Deterrence is most of the value, which is why MCP_SERVER.md says out loud
// that this exists. Watermarking that nobody knows about deters nobody.
package mcp

import (
	"crypto/sha256"
	"encoding/hex"
)

// closings are semantically identical. Each says: this is one client's copy,
// and the numbers are the same for everyone.
var closings = []string{
	" This copy is attributable to the requesting client; the figures are identical for every client.",
	" The figures here are identical for every client; this particular copy is attributable to the requester.",
	" Figures do not vary by client. This copy is attributable to the client that requested it.",
	" This is an attributable copy. No figure in it varies between clients.",
}

// watermark stamps a per-client mark onto an already-sanitized payload. It
// runs after the allowlist, so everything it adds is server-generated
// constant text and a hash — nothing derived from the dataset.
func watermark(clientID, tool string, payload map[string]any) map[string]any {
	if payload == nil {
		return payload
	}
	h := sha256.Sum256([]byte("signaldeck-mcp-wm|" + clientID))
	tag := hex.EncodeToString(h[:4])
	variant := int(h[4]) % len(closings)

	if d, ok := payload["disclaimer"].(string); ok {
		payload["disclaimer"] = d + closings[variant]
	}
	// Rotate order-insignificant string lists. Rotation, not shuffling: it is
	// reversible, so an operator can normalise a leaked copy before comparing.
	if rt, ok := payload["relatedTopics"].([]any); ok && len(rt) > 1 {
		payload["relatedTopics"] = rotate(rt, int(h[5])%len(rt))
	}
	payload["_provenance"] = map[string]any{
		"clientTag": tag,
		"notice": "Responses from this server are watermarked per client through insignificant " +
			"formatting only — never through a number, band, sample size or caveat. Redistribution " +
			"of this output is traceable.",
	}
	return payload
}

func rotate(in []any, n int) []any {
	if len(in) == 0 {
		return in
	}
	n %= len(in)
	out := make([]any, 0, len(in))
	out = append(out, in[n:]...)
	out = append(out, in[:n]...)
	return out
}
