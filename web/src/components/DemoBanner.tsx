// The demo-data banner. Renders only in a build made with NEXT_PUBLIC_DEMO_DATA=1
// (next.config.ts inlines the text through `env`; in any other build it is "" and
// this returns nothing). Fixed to the bottom of the viewport so it is in every
// frame of a recording, with a spacer so it never hides the end of a page.
export default function DemoBanner() {
  const text = process.env.SIGNALDECK_DEMO_BANNER;
  if (!text) return null;
  return (
    <>
      <div aria-hidden="true" style={{ height: "4.5rem", flexShrink: 0 }} />
      <div
        role="status"
        data-demo-data="1"
        style={{
          position: "fixed",
          left: 0,
          right: 0,
          bottom: 0,
          zIndex: 2147483647,
          background: "#ffd400",
          color: "#000",
          borderTop: "3px solid #000",
          padding: "0.55rem 1rem",
          fontFamily: "Arial, Helvetica, sans-serif",
          fontSize: "14px",
          fontWeight: 800,
          lineHeight: 1.35,
          textAlign: "center",
        }}
      >
        {text}
      </div>
    </>
  );
}
