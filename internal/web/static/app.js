const run = document.querySelector("[data-run-id]");
const profileSearch = document.querySelector("[data-profile-search]");
if (profileSearch) {
  const rows = [...document.querySelectorAll("[data-profile-row]")];
  profileSearch.addEventListener("input", () => {
    const query = profileSearch.value.trim().toLocaleLowerCase();
    let visible = 0;
    for (const row of rows) {
      row.hidden = !row.textContent.toLocaleLowerCase().includes(query);
      if (!row.hidden) visible++;
    }
    document.querySelector("[data-profile-empty]").hidden = visible > 0;
  });
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
