We are writing a Next.js 16 App Router page at `web/src/app/risk/page.tsx`
 Requirements:
 1. Server component: export const dynamic = "force-dynamic";
 2. Export a metadata object with title and description.
 3. Default export: export default async function RiskPage()
 4. Fetch from daemon: 
      const daemon = process.env.SIGNALDECK_DAEMON || "http://127.0.0.1:8322";
      const res = await fetch(daemon + "/api/vol-forecast/record", { cache: "no-store" });
    Wrap in try/catch. On any failure (throw or !res.ok) render an honest unavailable state and return early.
 5. Define TypeScript types for the response shape (all numbers may be null).
 6. Page structure:
    a. Heading and plain-English paragraph (non-expert) about what is measured.
    b. Caveat box (from response.caveat) at the TOP, before any number, as an amber/warning panel.
    c. One card per horizon (from response.horizons). Each card:
        - Title: e.g., "1 session ahead", "5 sessions ahead" (based on horizon value)
        - Verdict badge: prominent, "INSUFFICIENT" must be LOUD (amber or red border and text). 
          We'll use: if verdict === "INSUFFICIENT" then use bad/warn colors, else if "ACCRUING" then use accent/ok? 
          But note: the verdict can only be "INSUFFICIENT" or "ACCRUING" per the type.
        - verdictReason text
        - "distinctDays of minDistinctDays trading days" with a progress bar (capped at 100%)
        - n and ungradable as small secondary figures (labelled)
        - WHEN sufficient is true: render the three mean QLIKE values (har, rw, ewma) and the two comparisons (vsEwma, vsRandomWalk)
          When sufficient is false: do not render these at all.
        - When comparisons are shown, label the sign: e.g., "negative means our forecast had lower loss than the simple rule"
    d. Closing paragraph: verdict of skill can only come from the pre-registered grader.

 Styling:
   - Use CSS variables: var(--panel) var(--border) var(--border-strong) var(--text) var(--dim) var(--faint) var(--accent) var(--bad) var(--ok) var(--warn)
   - Existing classes: panel (bordered card), chip (bordered pill), mono (monospace), tnum (tabular numerals) -> put tnum on EVERY number.
   - Tailwind utilities for layout, spacing, type.
   - Dark background is already applied by layout -> do not set page background.
   - Cap prose width: max-w-[62ch] or max-w-[70ch].

 Quality bars:
   - TypeScript strict: no any, no non-null assertions.
   - Escape apostrophes in JSX as HTML entity (because of react rules lint).
   - Every list rendered with .map() and stable key.
   - Format numbers to sensible fixed precision (we'll use toFixed(2) or similar, but note: some may be null).
   - Must compile under tsc --noEmit.
   - Keep under about 260 lines.

 Steps:

 1. Define the types for the response.
 2. In the server component:
      - Set dynamic = "force-dynamic"
      - Define metadata
      - In the async function:
          * Get daemon URL from env or default.
          * Fetch with cache: "no-store"
          * Try/catch: if error or !res.ok, render unavailable state and return.
          * Parse JSON.
          * Then render the page.

 3. Unavailable state: we'll show a message that the data is unavailable.

 4. For the caveat box: we'll use a div with classes that use the CSS variables for an amber/warning panel.
    Since we don't have the exact CSS, we'll assume:
        --panel: background color for panel
        --border: border color
        --border-strong: stronger border
        --text: text color
        --dim: dim text
        --faint: faint text
        --accent: accent color (for good)
        --bad: bad color (for errors/insufficient)
        --warn: warning color (for caveat)

    We are told to use an amber/warning panel for the caveat. So we can use:
        border color: var(--warn)
        background: var(--panel) maybe? or a lighter warn? But we don't have specifics.
        We'll do: 
          className="border border-warn bg-panel/50 text-warn" 
        but note: we are to use CSS variables, so we cannot use Tailwind's color classes directly? 
        However, the problem says: use ONLY these CSS variables and the existing classes (panel, chip, mono, tnum) and Tailwind utilities.

    We are allowed to use Tailwind utilities for layout, spacing, type. So we can do:

        <div className="panel border border-warn bg-panel/50 text-warn p-4 mb-6">
          {caveat}
        </div>

    But note: the existing class `panel` is a bordered card. We are adding more classes.

    However, the problem says: "Style it as a bordered amber/warning panel". We'll use:
        border: var(--border-strong) maybe? but we are to use the CSS variables in the style? 
        Actually, we are to use the CSS variables in our CSS, but in the JSX we can use Tailwind to set the border color to the CSS variable? 
        But Tailwind doesn't let us use CSS variables directly in class names without using arbitrary values.

    Since we are to use the CSS variables, and we are allowed to use Tailwind utilities, we can do:

        <div className="panel border border-[var(--border-strong)] bg-[var(--panel)] text-[var(--warn)] p-4 mb-6">
          {caveat}
        </div>

    However, the problem says: "use ONLY these, invent no design system", meaning we should not invent new classes, but we can use Tailwind's arbitrary values? 
    But note: the problem says "Existing classes: panel (a bordered card), chip (a bordered pill), mono (monospace), tnum (tabular numerals)".

    We are allowed to use Tailwind utilities for layout, spacing, type. So we can use arbitrary values for colors? 
    However, the problem does not explicitly forbid arbitrary values, but it says "use ONLY these" (meaning the CSS variables and the existing classes). 
    So we must use the CSS variables via inline style? or via class that uses the variable? 

    Since we are in a Next.js app with Tailwind, and we are allowed to use Tailwind utilities, we can use:

        <div className="panel border border-warn bg-panel/50 text-warn p-4 mb-6" 
             style={{ borderColor: 'var(--border-strong)', backgroundColor: 'var(--panel)', color: 'var(--warn)' }}>

    But that mixes Tailwind and inline style. Alternatively, we can rely on the fact that the CSS variables are defined and use:

        <div className="panel border border-[var(--border-strong)] bg-[var(--panel)/50] text-[var(--warn)] p-4 mb-6">

    However, Tailwind does not support CSS variables in class names by default without using the bracket notation. 
    And the problem says we can use Tailwind utilities. The bracket notation is a Tailwind feature (arbitrary values).

    Given the constraints, I think we are allowed to use arbitrary values for the CSS variables. 
    But note: the problem says "use ONLY these" (the CSS variables and the existing classes). 
    So we are using the CSS variables via arbitrary values in Tailwind, which is acceptable.

    However, to be safe and because the problem says "Existing classes: panel", we can also note that the `panel` class might already set some border and background. 
    We are overriding with our own.

    Let's break down the caveat box:
        - We want a bordered panel (so we use the `panel` class which is a bordered card) but then we want to change the border color to warn and the text color to warn.
        - We'll do: 
            className="panel border border-warn text-warn bg-panel/50 p-4 mb-6"

    But wait, the problem says to use the CSS variables. So we must use the variables, not the Tailwind color names. 
    Therefore, we have to use arbitrary values to reference the CSS variables.

    We'll do:

        <div className="panel border border-[var(--border-strong)] bg-[var(--panel)/50] text-[var(--warn)] p-4 mb-6">
          {caveat}
        </div>

    However, note: the problem says "amber/warning panel", and we have a variable `--warn` for warning.

 5. For each horizon card:
        - We'll use the `panel` class for the card.
        - Inside:
            * Title: e.g., `${horizon.horizon} session${horizon.horizon !== 1 ? 's' : ''} ahead`
            * Verdict badge: 
                We'll use the `chip` class (a bordered pill) and then set colors based on verdict.
                For "INSUFFICIENT": we want it LOUD -> use bad/warn colors? 
                We'll use: 
                  if (verdict === 'INSUFFICIENT') {
                    className="chip border-border-strong text-bad bg-bad/10"
                  } else {
                    // ACCRUING
                    className="chip border-border-strong text-accent bg-accent/10"
                  }
                But again, we must use CSS variables via arbitrary values? 
                However, the problem says we have existing class `chip` (a bordered pill). 
                We are allowed to use Tailwind utilities for layout, spacing, type. 
                So we can do:

                  <span className={`chip border-[var(--border-strong)] ${verdict === 'INSUFFICIENT' ? 'text-[var(--bad)] bg-[var(--bad)/10]' : 'text-[var(--accent)] bg-[var(--accent)/10]'}`}>
                    {verdict}
                  </span>

            * verdictReason: just text, we can use dim or faint? 
                className="text-dim mt-1"
            * Progress bar for distinctDays/minDistinctDays:
                We'll show: 
                  <div className="flex items-center mt-2">
                    <span className="tnum mr-2">{distinctDays}</span> of 
                    <span className="tnum">{minDistinctDays}</span> trading days
                  </div>
                Then a progress bar:
                  <div className="w-full bg-faint rounded h-2 mt-1">
                    <div 
                      className={`bg-accent h-2 rounded`} 
                      style={{ width: Math.min((distinctDays / minDistinctDays) * 100, 100) + '%' }}
                    ></div>
                  </div>
                But note: we must cap at 100%. We are using Math.min.

            * n and ungradable: 
                  <div className="text-faint text-sm mt-2 flex gap-4">
                    <span>n: <span className="tnum">{n}</span></span>
                    <span>ungradable: <span className="tnum">{ungradable}</span></span>
                  </div>

            * When sufficient is true:
                  We show:
                    mean QLIKE: 
                      har: {meanQlike.har?.toFixed(2) ?? null} -> but we cannot show null, we show nothing? 
                      Actually, the type says they may be null. We are to format numbers to fixed precision, but if null we skip? 
                      However, the requirement: "Format numbers to a sensible fixed precision; never print a raw float with 17 digits."
                      So if the number is null, we don't show it? But the requirement says: when sufficient is true, we show the three mean QLIKE values and the two comparisons.
                      But if they are null, we should show nothing? or show null? 
                      The problem says: "all numbers may be null". And we are to format numbers. 
                      We'll do: if the number is not null, format it to 2 decimal places, else show nothing? 
                      But the requirement says: "WHEN AND ONLY WHEN sufficient is true: the three mean QLIKE values and the two comparisons." 
                      It doesn't say we must show them even if null. So we can conditionally render only if not null? 
                      However, the problem says: "not as a dash, not as zero, not greyed out. Absent." 
                      So if the value is null, we do not render that particular number at all.

                  We'll break it down:

                    <div className="mt-4 text-sm space-y-2">
                      <div className="flex flex-col">
                        <span className="font-medium">mean QLIKE:</span>
                        <div className="flex gap-4 text-faint">
                          {meanQlike.har !== null && (
                            <span>har: <span className="tnum">{meanQlike.har.toFixed(2)}</span></span>
                          )}
                          {meanQlike.rw !== null && (
                            <span>rw: <span className="tnum">{meanQlike.rw.toFixed(2)}</span></span>
                          )}
                          {meanQlike.ewma !== null && (
                            <span>ewma: <span className="tnum">{meanQlike.ewma.toFixed(2)}</span></span>
                          )}
                        </div>
                      </div>
                      <div className="flex gap-4 text-faint">
                        {vsEwma !== null && (
                          <span>vs Ewma: <span className="tnum">{vsEwma.toFixed(2)}</span> 
                            {vsEwma < 0 ? '(negative means our forecast had lower loss)' : '(positive means our forecast had higher loss)'}
                          </span>
                        )}
                        {vsRandomWalk !== null && (
                          <span>vs Random Walk: <span className="tnum">{vsRandomWalk.toFixed(2)}</span> 
                            {vsRandomWalk < 0 ? '(negative means our forecast had lower loss)' : '(positive means our forecast had higher loss)'}
                          </span>
                        )}
                      </div>
                    </div>

                  But note: the requirement says: "label the sign explicitly, e.g., 'negative means our forecast had lower loss than the simple rule'"
                  We are labeling the sign in parentheses.

            * However, note: the two comparisons are vsEwma and vsRandomWalk. We are to show them only when sufficient is true.

 6. Closing paragraph: 
        <p className="mt-6 text-faint">
          A verdict of skill can only come from the pre-registered grader, not from this page.
        </p>

 7. We must escape apostrophes in JSX as HTML entity. 
    Example: if we have a string with an apostrophe, we replace ' with &#x27; 
    But note: we are getting strings from the API (like caveat, verdictReason). 
    We must escape them when rendering in JSX? 
    However, in React, if we render a string as text content (not as attribute), it is automatically escaped. 
    But the problem says: "Escape apostrophes in JSX as the HTML entity, because the project lints react rules."
    This likely means that if we are using the string in an attribute (like in a className that we build conditionally) we must escape? 
    But note: we are not putting the string in an attribute, we are putting it in the JSX text. 
    However, to be safe, we can use a helper function to escape the string for HTML? 
    But React does that for us when we use {string} in JSX (as text). 
    The problem might be referring to when we use the string in an attribute? 
    Example: 
        <div title={someString}> 
    Then we must escape? 
    But React does not escape attribute values? Actually, it does: 
        https://reactjs.org/docs/dom-elements.html#dangerouslysetinnerhtml
        But for attributes, React escapes the string.

    However, the problem says: "because the project lints react rules". 
    There is a rule (react/jsx-no-target-blank) but also maybe react/jsx-no-constructed-context? 
    Actually, there is a rule for escaping: react/jsx-no-literals? 
    I think they are referring to the fact that if we use a string in an attribute that is not a standard attribute, we might need to escape? 
    But we are not doing that.

    Alternatively, they might be referring to the fact that we are building classNames conditionally and we are using string concatenation? 
    But we are using template literals and ternary, which is safe.

    Given the ambiguity, and since the problem says "Escape apostrophes in JSX as the HTML entity", 
    we will escape any string that we are putting into JSX as text content by replacing ' with &#x27; 
    and also " with &quot;? 
    But note: the problem only mentions apostrophes.

    However, in React, if we do:
        <span>{someString}</span>
    and someString contains an apostrophe, it is safe because React escapes the text content.

    But to follow the instruction literally, we will escape apostrophes in the strings we get from the API when we are about to render them as text.

    We'll create a helper function (but note: we are to write the least code, and we are not to add unrequested abstractions). 
    Since we are only using the strings in a few places, we can do inline replace.

    However, note: we are in a server component, and we are returning JSX. 
    We can do:

        {verdictReason.replace(/'/g, "&#x27;")}

    But wait: what if the string has other HTML? We are not allowed to have HTML because it's plain text. 
    The API returns a string for caveat and verdictReason, and we are to render them verbatim (but escaped for safety). 
    Since we are in a text context, we should escape the string to prevent XSS. 
    However, React does that by default. 

    Given the confusion, and because the problem says "because the project lints react rules", 
    I think they are referring to a specific lint rule that flags unescaped apostrophes in JSX strings? 
    But that doesn't make sense because JSX strings are not the same as HTML strings.

    After re‐reading: "Escape apostrophes in JSX as the HTML entity" 
    This might mean: when you write a string literal in JSX (like in an attribute) and that string contains an apostrophe, 
    you must escape it as &#x27; so that it doesn't break the JSX? 
    Example: 
        <div className='foo-bar'> 
    is okay, but 
        <div className='foo's bar'> 
    is not okay because the apostrophe closes the string.

    So in our JSX, if we have an attribute value that is a string literal and contains an apostrophe, we must escape it. 
    But we are not using string literals with apostrophes in our attributes? 
    We are using variables. 

    However, we are building classNames conditionally with string literals that do not contain apostrophes. 
    So we are safe.

    But note: the caveat and verdictReason strings might contain apostrophes. 
    When we render them as text content (not in an attribute), we don't need to escape for JSX syntax, but we do for HTML safety. 
    And React does that for us.

    Therefore, I think we don't need to do anything. 
    However, to strictly follow the instruction, we will escape apostrophes in the strings we are about to render as text content by replacing ' with &#x27; 
    and also we'll escape quotes? The instruction only says apostrophes.

    We'll do: 
        const escapeApos = (s: string) => s.replace(/'/g, "&#x27;");

    But note: we are to write the least code. Adding a helper function might be more than one line? 
    We can do inline: 
        {caveat.replace(/'/g, "&#x27;")}

    However, we are using the caveat in multiple places? No, only once. 
    Similarly, verdictReason only once.

    But note: we are also using the horizon.horizon in a string for the title? 
        `${horizon.horizon} session${horizon.horizon !== 1 ? 's' : ''} ahead`
    This string is built by us and does not contain apostrophes from the data, so safe.

    So we'll escape the caveat and verdictReason.

 8. Let's write the code accordingly.

 9. We must define the types for the response.

 10. We must format numbers: 
        For the progress bar: we are using distinctDays and minDistinctDays (integers) so no formatting needed.
        For n and ungradable: integers.
        For the mean QLIKE and comparisons: if not null, format to 2 decimal places.

 11. We must put tnum on EVERY number. 
        We'll put the className="tnum" on every span that contains a number.

 12. We must avoid any and non-null assertions.

 13. We must return early on error.

 Implementation:

   export const dynamic = "force-dynamic";

   export const metadata = {
     title: "Volatility Forecast Record",
     description: "Live record of a new volatility forecast that has just started accruing data."
   };

   export default async function RiskPage() {
     const daemon = process.env.SIGNALDECK_DAEMON || "http://127.0.0.1:8322";
     let data;
     try {
       const res = await fetch(`${daemon}/api/vol-forecast/record`, { cache: "no-store" });
       if (!res.ok) {
         throw new Error(`HTTP error! status: ${res.status}`);
       }
       data = await res.json();
     } catch (err) {
       // Render unavailable state
       return (
         <div className="max-w-[62ch] mx-auto py-8">
           <h1 className="mb-4 text-2xl font-bold">Volatility Forecast Record</h1>
           <p className="text-faint">
             Unfortunately, the volatility forecast data is currently unavailable.
             Please try again later.
           </p>
         </div>
       );
     }

     // Now we have data, which should match our type
     const { asOf, evidence, minDistinctDays, horizons, caveat } = data;

     // We'll escape the caveat and verdictReason in each horizon for apostrophes
     const escapedCaveat = caveat.replace(/'/g, "&#x27;");

     return (
       <div className="max-w-[62ch] mx-auto py-8">
         <h1 className="mb-4 text-2xl font-bold">Volatility Forecast Record</h1>
         <p className="mb-6 text-faint">
           This page forecasts how much a stock is likely to move over the next few sessions.
           It compares our forecast against two simple rules: 
           {' '}
           <span className="mono">the historical average (HA)</span> and 
           <span className="mono">a random walk (RW)</span>.
           Lower loss means our forecast is better.
         </p>

         {/* Caveat box at the TOP */}
         <div className="panel border border-[var(--border-strong)] bg-[var(--panel)/50] text-[var(--warn)] p-4 mb-6">
           {escapedCaveat}
         </div>

         {/* Horizons cards */}
         <div className="space-y-6">
           {horizons.map((h) => {
             const escapedVerdictReason = h.verdictReason.replace(/'/g, "&#x27;");
             return (
               <div key={h.horizon} className="panel p-6">
                 <div className="flex justify-between items-start mb-4">
                   <h2 className="text-xl font-bold">
                     {h.horizon} session{h.horizon !== 1 ? 's' : ''} ahead
                   </h2>
                   <span className={`chip border-[var(--border-strong)] 
                     ${h.verdict === 'INSUFFICIENT' 
                       ? 'text-[var(--bad)] bg-[var(--bad)/10]' 
                       : 'text-[var(--accent)] bg-[var(--accent)/10]'}`}>
                     {h.verdict}
                   </span>
                 </div>

                 <p className="text-dim mb-2">{escapedVerdictReason}</p>

                 {/* Progress bar */}
                 <div className="mb-4">
                   <div className="flex items-center mb-1">
                     <span className="tnum mr-2">{h.distinctDays}</span> of 
                     <span className="tnum">{minDistinctDays}</span> trading days
                   </div>
                   <div className="w-full bg-faint rounded h-2">
                     <div 
                       className="bg-accent h-2 rounded" 
                       style={{ 
                         width: Math.min((h.distinctDays / minDistinctDays) * 100, 100) + '%' 
                       }}
                     ></div>
                   </div>
                 </div>

                 {/* n and ungradable */}
                 <div className="text-faint text-sm mb-4 flex gap-4">
                   <span>n: <span className="tnum">{h.n}</span></span>
                   <span>ungradable: <span className="tnum">{h.ungradable}</span></span>
                 </div>

                 {/* When sufficient is true, show the metrics */}
                 {h.sufficient && (
                   <>
                     <div className="mb-4 text-sm space-y-2">
                       <div className="flex flex-col">
                         <span className="font-medium">mean QLIKE:</span>
                         <div className="flex gap-4 text-faint">
                           {h.meanQlike.har !== null && (
                             <span>har: <span className="tnum">{h.meanQlike.har.toFixed(2)}</span></span>
                           )}
                           {h.meanQlike.rw !== null && (
                             <span>rw: <span className="tnum">{h.meanQlike.rw.toFixed(2)}</span></span>
                           )}
                           {h.meanQlike.ewma !== null && (
                             <span>ewma: <span className="tnum">{h.meanQlike.ewma.toFixed(2)}</span></span>
                           )}
                         </div>
                       </div>
                       <div className="flex gap-4 text-faint">
                         {h.vsEwma !== null && (
                           <span>vs Ewma: <span className="tnum">{h.vsEwma.toFixed(2)}</span> 
                             {h.vsEwma < 0 
                               ? '(negative means our forecast had lower loss)' 
                               : '(positive means our forecast had higher loss)'}
                           </span>
                         )}
                         {h.vsRandomWalk !== null && (
                           <span>vs Random Walk: <span className="tnum">{h.vsRandomWalk.toFixed(2)}</span> 
                             {h.vsRandomWalk < 0 
                               ? '(negative means our forecast had lower loss)' 
                               : '(positive means our forecast had higher loss)'}
                           </span>
                         )}
                       </div>
                     </div>
                   </>
                 )}
               </div>
             );
           })}
         </div>

         {/* Closing paragraph */}
         <p className="mt-6 text-faint">
           A verdict of skill can only come from the pre-registered grader, not from this page.
         </p>
       </div>
     );
   }

 14. We must define the TypeScript types for the response.

     We'll define an interface for the horizon item and the main response.

     We can do it inside the file.

     Note: the asOf is a number (timestamp?).

     We'll put the types above the component.

 15. We must avoid any: so we'll type the data variable as the response type.

 16. We must note: the evidence field is a string, but we don't use it in the UI? 
     The requirement says: 
        THE RESPONSE SHAPE (define TypeScript types; all numbers may be null):
        {
          asOf: number,
          evidence: string,
          minDistinctDays: number,
          horizons: [ ... ],
          caveat: string
        }

     We are not using `evidence` and `asOf` in the UI? 
     The requirement for the page structure does not mention them. 
     So we don't render them.

 17. We are using minDistinctDays in the progress bar for each horizon.

 18. We are using the horizons array.

 19. We are using the caveat.

 20. We are not using asOf and evidence.

 21. Let's write the types.

 22. We must format numbers: we are using toFixed(2) for the mean QLIKE and comparisons. 
     But note: if the number is null, we skip. 
     We are also using the horizon.horizon (integer) in the title, and we are putting tnum on it? 
        We have: 
          <h2 className="text-xl font-bold">
            {h.horizon} session{h.horizon !== 1 ? 's' : ''} ahead
          </h2>
        The horizon.horizon is a number, but we are not wrapping it in a span with tnum? 
        We must put tnum on EVERY number. 
        So we should do:
          <span className="tnum">{h.horizon}</span> session{h.horizon !== 1 ? 's' : ''} ahead

     Similarly, for the distinctDays and minDistinctDays in the progress bar label, we already have tnum.

     For n and ungradable: we have tnum.

     For the mean QLIKE and comparisons: we have tnum.

 23. We must also put tnum on the asOf and evidence if we rendered them? But we don't.

 24. Let's adjust the title to put tnum on the horizon number.

 25. We must also note: the progress bar uses distinctDays and minDistinctDays, which we already have tnum on.

 26. We must avoid non-null assertions: we are checking for null before calling toFixed.

 27. We must avoid any: we are typing the data.

 28. We must note: the horizons array is non-empty? We don't know, but we map over it.

 29. We must note: the minDistinctDays might be zero? 
        Then we would get division by zero. 
        We are doing: (h.distinctDays / minDistinctDays) * 100
        If minDistinctDays is 0, then we get Infinity. 
        We should avoid that.

     The problem says: minDistinctDays is a number (may be null? but in the type we defined it as number, not number|null). 
     Wait: the type definition says: 
          minDistinctDays: number,

     But the problem says: "all numbers may be null" in the response shape. 
     So minDistinctDays may be null? 

     Let me check: 
        "horizons: [ { ... minDistinctDays: number, ... } ]"

     Actually, the minDistinctDays is outside the horizons array? 
        {
          asOf: number,
          evidence: string,
          minDistinctDays: number,
          horizons: [ ... ],
          caveat: string
        }

     So minDistinctDays is a top-level field and may be null? 

     The problem says: "all numbers may be null". 

     Therefore, we must treat minDistinctDays as number | null.

     Similarly, asOf and evidence? evidence is string, so not null? but the problem says "all numbers may be null", so evidence is string and may be null? 
     But the type says evidence: string. 

     We'll adjust the types to have numbers as number | null where appropriate.

     However, note: the problem says "all numbers may be null", meaning the fields that are numbers in the shape may be null. 
     So:
        asOf: number | null
        minDistinctDays: number | null
        evidence: string   (not a number, so not subject to the null? but the problem says "all numbers", so evidence is string and may be present or not? 
        but the problem does not say strings may be null. We'll assume evidence is string and not null? 
        However, to be safe, we'll follow the shape: the problem says the shape has evidence: string. 
        So we'll keep it as string.

     But note: the problem says "all numbers may be null", so we only change the number fields to number | null.

     Therefore:
        asOf: number | null
        minDistinctDays: number | null
        horizons: Array<{
          horizon: number,   // may be null? 
          n: number | null,
          distinctDays: number | null,
          ungradable: number | null,
          sufficient: boolean,   // boolean is not a number, so not null? 
          verdict: string,       // not a number
          verdictReason: string, // not a number
          meanQlike: { har: number | null, rw: number | null, ewma: number | null },
          vsEwma: number | null,
          vsRandomWalk: number | null,
          lowerIsBetter: boolean   // not a number
        }>

     However, the horizon (the key for the horizon) is used to build the title. 
        If horizon is null, we cannot build the title. 
        But the problem says: horizons is an array of objects with horizon: number. 
        And the problem says "all numbers may be null", so horizon may be null? 

     We must handle nulls in the data.

     Steps for nulls:
        - If minDistinctDays is null, we cannot compute the progress bar. We'll show "N/A" or skip? 
          But the requirement: we must show the progress bar only if we have the data? 
          However, the requirement does not specify. 
          We are to show: "distinctDays of minDistinctDays trading days"
          If either is null, we cannot show the numbers. 
          We'll show: 
            distinctDays: if null -> "N/A", else the number
            minDistinctDays: if null -> "N/A", else the number

        - Similarly, for the horizon number in the title: if null, we show "N/A" session? 
          But that doesn't make sense. 
          We'll assume that horizon is never null because it's the key for the array? 
          But the type says it may be null. 

     Given the complexity and the fact that the problem says "LIVE record of a new volatility forecast that has only just started accruing data", 
     it is likely that the numbers are present but small. 
     However, we must handle nulls.

     We'll do:

        For the horizon title:
          {h.horizon !== null ? (
            <span className="tnum">{h.horizon}</span> session{h.horizon !== 1 ?