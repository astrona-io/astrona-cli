/* Decorative motion is optional. Content never waits for this script. */
(() => {
  const reduced = window.matchMedia("(prefers-reduced-motion: reduce)");
  const key = "astrona:docs-motion-paused";
  let paused = false;
  try {
    paused = localStorage.getItem(key) === "true";
  } catch {
    /* Storage can be disabled. */
  }

  function render() {
    const stopped = paused || reduced.matches;
    document.documentElement.classList.toggle("astro-motion-paused", stopped);
    document.querySelectorAll(".astro-motion").forEach((button) => {
      button.hidden = false;
      button.disabled = reduced.matches;
      button.setAttribute("aria-pressed", String(stopped));
      button.textContent = reduced.matches
        ? "Reduced motion enabled"
        : paused
          ? "Play animations"
          : "Pause animations";
    });
  }

  document.addEventListener("click", (event) => {
    if (
      !(event.target instanceof Element) ||
      !event.target.closest(".astro-motion")
    )
      return;
    paused = !paused;
    try {
      localStorage.setItem(key, String(paused));
    } catch {
      /* Keep the choice for this page. */
    }
    render();
  });
  reduced.addEventListener("change", render);
  window.addEventListener("storage", (event) => {
    if (event.key !== key && event.key !== null) return;
    paused = event.newValue === "true";
    render();
  });
  render();
})();
