import { ImageResponse } from "next/og";
import { OG_IMAGE } from "@/lib/site";

/**
 * The social card, served at /og-image.
 *
 * A plain route, not the opengraph-image file convention, since 2026-10-02
 * (B6): the convention attaches og:image to every page whatever the build
 * knows, and Next makes it absolute against http://localhost:$PORT when no
 * metadataBase is set, so a build without NEXT_PUBLIC_SITE_URL shipped
 * localhost in og:image and twitter:image. layout.tsx now attaches this card
 * only when the site has a name (lib/site.ts).
 *
 * Written against
 * node_modules/next/dist/docs/01-app/03-api-reference/03-file-conventions/
 * 01-metadata/opengraph-image.md, because web/AGENTS.md says this is not the
 * Next.js I know and the error boundary already proved it by taking `retry`
 * where I had written `reset`.
 *
 * No custom font is loaded. The documented example reads a .ttf off disk, and
 * this repository ships none; adding one to render a share card would be a
 * new asset and a new failure mode for no gain. The system stack is fine at
 * this size.
 *
 * The copy is the same claim the landing page makes, and deliberately not a
 * number: a figure baked into a cached social image is a figure that goes
 * stale silently, which is the exact defect that put three different accuracy
 * values into circulation at once.
 */
// Rendered once at build, as the file convention was.
export const dynamic = "force-static";

export function GET() {
  return new ImageResponse(
    (
      <div
        style={{
          width: "100%",
          height: "100%",
          display: "flex",
          flexDirection: "column",
          justifyContent: "center",
          padding: "80px",
          background: "#060910",
          color: "#e8eef6",
          fontFamily: "system-ui, sans-serif",
        }}
      >
        <div
          style={{
            display: "flex",
            fontSize: 26,
            letterSpacing: 6,
            color: "#fbbf24",
            textTransform: "uppercase",
          }}
        >
          SignalDeck
        </div>
        <div
          style={{
            display: "flex",
            marginTop: 28,
            fontSize: 82,
            fontWeight: 800,
            lineHeight: 1.05,
            maxWidth: 900,
          }}
        >
          A market instrument that grades itself in public.
        </div>
        <div
          style={{
            display: "flex",
            marginTop: 32,
            fontSize: 32,
            color: "#94a3b8",
            maxWidth: 900,
          }}
        >
          Including the claims that failed. Especially those.
        </div>
      </div>
    ),
    { width: OG_IMAGE.width, height: OG_IMAGE.height },
  );
}
