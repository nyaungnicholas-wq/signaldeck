// TradingView's free advanced-chart embed, mounted so its script never loses
// its parent. The embed script finds its widget through its OWN parentNode
// when it runs (_replaceScript: scriptElement.parentNode.querySelector(...)).
// Detaching the script before it ran (container.replaceChildren() in an effect
// cleanup, which React's dev double-mount and any fast navigation or symbol
// change trigger) left parentNode null, and every symbol page logged
// "Cannot read properties of null (reading 'querySelector')". Each mount now
// owns a wrapper holding the widget div and the script, and cleanup removes
// the wrapper whole: the script keeps its parent, so a late run lands in the
// detached wrapper instead of throwing. The script itself goes in on the next
// task, so a mount React discards at once (the dev double-mount) never loads
// it: no widget is built into a detached wrapper, where TradingView logs
// "Cannot listen to the event from the provided iframe".

export const TV_EMBED_SRC = "https://s3.tradingview.com/external-embedding/embed-widget-advanced-chart.js";

/** Mounts one widget in container and returns its cleanup. doc is a seam for tests. */
export function mountTradingViewWidget(container: HTMLElement, config: object, doc: Document = document): () => void {
  const wrapper = doc.createElement("div");
  wrapper.className = "tradingview-widget-container";
  wrapper.style.height = "100%";
  wrapper.style.width = "100%";

  const widget = doc.createElement("div");
  widget.className = "tradingview-widget-container__widget";
  widget.style.height = "100%";
  widget.style.width = "100%";
  wrapper.appendChild(widget);

  const script = doc.createElement("script");
  script.src = TV_EMBED_SRC;
  script.type = "text/javascript";
  script.async = true;
  script.textContent = JSON.stringify(config);

  container.appendChild(wrapper);
  const timer = setTimeout(() => wrapper.appendChild(script), 0);
  return () => {
    clearTimeout(timer);
    wrapper.remove();
  };
}
