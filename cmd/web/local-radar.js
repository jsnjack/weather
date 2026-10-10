(function () {
  const panel = document.getElementById("local-radar");
  if (!panel) return;
  const query = new URLSearchParams(panel.dataset.location);
  const params = new URLSearchParams();
  ["lat", "lon", "name", "label"].forEach(function (key) {
    if (query.has(key)) params.set(key, query.get(key));
  });
  fetch("/api/v1/radar/local?" + params.toString()).then(async function (response) {
    if (response.status === 204 || response.status === 404) return;
    if (!response.ok) throw new Error("Local radar: HTTP " + response.status);
    const time = new Date(response.headers.get("X-Radar-Observed-At"));
    const age = Date.now() - time.getTime();
    if (!Number.isFinite(age) || age < -120000 || age > 1200000) return;
    const blobURL = URL.createObjectURL(await response.blob());
    const img = panel.querySelector("img");
    img.src = blobURL;
    try { await img.decode(); } finally { URL.revokeObjectURL(blobURL); }
    panel.hidden = false;
    if (window.location.hash === "#local-radar") panel.scrollIntoView();
  }).catch(function (error) { console.debug("Local radar unavailable", error); });
})();
