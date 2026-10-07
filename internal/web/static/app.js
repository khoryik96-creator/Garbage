const run = document.querySelector("[data-run-id]");
const profileSearch = document.querySelector("[data-profile-search]");
document.querySelector("[data-copy-url]")?.addEventListener("click", async () => {
  const status = document.querySelector("[data-copy-status]");
  try {
    await navigator.clipboard.writeText(window.location.origin + "/");
    status.textContent = "Address copied.";
  } catch (_) {
    status.textContent = window.location.origin + "/";
  }
});
if (profileSearch) {
  const savedQuery = new URLSearchParams(window.location.search).get("q");
  if (savedQuery !== null) profileSearch.value = savedQuery;
  const rows = [...document.querySelectorAll("[data-profile-row]")];
  const filterProfiles = () => {
    const query = profileSearch.value.trim().toLocaleLowerCase();
    let visible = 0;
    for (const row of rows) {
      row.hidden = !row.textContent.toLocaleLowerCase().includes(query);
      if (!row.hidden) visible++;
    }
    document.querySelector("[data-profile-empty]").hidden = visible > 0;
  };
  profileSearch.addEventListener("input", () => {
    const address = new URL(window.location.href);
    address.searchParams.set("q", profileSearch.value);
    window.history.replaceState(null, "", address);
    filterProfiles();
  });
  filterProfiles();
  // pageshow covers normal history restoration and the back/forward cache.
  // The next frame also catches controls restored after the script executes.
  window.addEventListener("pageshow", () => requestAnimationFrame(filterProfiles));
}
if (run && ["queued", "running"].includes(run.dataset.runState)) {
  const timer = setInterval(async () => {
    // Preserve edits if the reviewer has started interacting with the form.
    if (document.activeElement?.matches("input, select, textarea")) return;
    try {
      const response = await fetch(`/api/runs/${run.dataset.runId}`);
      if (!response.ok) return;
      const state = await response.json();
      if (
        state.state !== run.dataset.runState ||
        state.processed !== Number(run.dataset.processed)
      ) {
        clearInterval(timer);
        window.location.reload();
      }
    } catch (_) {
      /* Keep the page readable during a temporary disconnect. */
    }
  }, 1500);
}
