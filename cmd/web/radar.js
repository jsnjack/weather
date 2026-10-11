(function () {
  const player = document.getElementById("radar-player");
  if (!player) return;
  const local = player.querySelector("[data-local-map]");
  const fallback = player.querySelector("[data-national-fallback]");
  const canvas = player.querySelector("canvas");
  const controls = player.querySelector(".radar-controls");
  const slider = controls.querySelector("input");
  const play = controls.querySelector("[data-play]");
  const latest = controls.querySelector("[data-latest]");
  const now = controls.querySelector("[data-now]");
  const output = controls.querySelector("output");
  const note = player.querySelector(".radar-note");
  const credit = player.querySelector(".radar-credit");
  const localButton = player.querySelector('[data-view="local"]');
  let mode = "national";
  let chosen = false;
  let localReady = false;
  let nationalLoad;
  let nationalFailed = false;
  let timer = null;
  let frame = 0;
  let timeline;
  let atlas;

  function pause() {
    clearTimeout(timer);
    timer = null;
    play.textContent = "Play";
    play.setAttribute("aria-pressed", "false");
  }

  function present() {
    local.hidden = mode !== "local" || !localReady;
    canvas.hidden = mode !== "national" || !timeline;
    fallback.hidden = mode !== "national" || !nationalFailed;
    controls.hidden = mode !== "national" || !timeline;
    credit.hidden = mode !== "local" || !localReady;
    note.hidden = mode !== "national" || !nationalFailed;
    player.querySelectorAll("[data-view]").forEach(function (button) {
      button.setAttribute("aria-pressed", String(button.dataset.view === mode));
    });
  }

  function select(view) {
    pause();
    mode = view;
    present();
    if (view === "national") loadNational();
  }

  function show(index) {
    frame = index;
    const x = (index % timeline.columns) * timeline.width;
    const y = Math.floor(index / timeline.columns) * timeline.height;
    const context = canvas.getContext("2d");
    context.clearRect(0, 0, canvas.width, canvas.height);
    context.drawImage(atlas, x, y, timeline.width, timeline.height, 0, 0, canvas.width, canvas.height);
    slider.value = index;
    output.textContent = (index + 1) + " / " + timeline.delays.length;
    slider.setAttribute("aria-valuetext", "Frame " + (index + 1) + " of " + timeline.delays.length);
    latest.disabled = index === timeline.delays.length - 1;
    now.disabled = index === timeline.latestObservation;
  }

  function advance() {
    show(frame + 1);
    if (frame === timeline.delays.length - 1) { pause(); return; }
    timer = setTimeout(advance, timeline.delays[frame]);
  }

  player.querySelectorAll("[data-view]").forEach(function (button) {
    button.addEventListener("click", function () { chosen = true; select(button.dataset.view); });
  });
  play.addEventListener("click", function () {
    if (!timeline || mode !== "national") return;
    if (timer !== null) { pause(); return; }
    if (frame === timeline.delays.length - 1) show(0);
    play.textContent = "Pause";
    play.setAttribute("aria-pressed", "true");
    timer = setTimeout(advance, timeline.delays[frame]);
  });
  slider.addEventListener("input", function () { pause(); show(Number(slider.value)); });
  latest.addEventListener("click", function () { pause(); show(timeline.delays.length - 1); });
  now.addEventListener("click", function () { pause(); show(timeline.latestObservation); });
  document.addEventListener("visibilitychange", function () { if (document.hidden) pause(); });
  window.addEventListener("pagehide", pause);

  function loadNational() {
    if (nationalLoad) return nationalLoad;
    nationalLoad = (async function () {
      try {
        const response = await fetch("/api/v1/radar", {cache: "no-store"});
        if (!response.ok) throw new Error("Radar timeline: HTTP " + response.status);
        const candidate = await response.json();
        if (!candidate.delays.length) throw new Error("Radar timeline has no frames");
        const image = new Image();
        image.src = candidate.atlasURL;
        await image.decode();
        timeline = candidate;
        atlas = image;
        canvas.width = timeline.width;
        canvas.height = timeline.height;
        slider.max = timeline.delays.length - 1;
        slider.disabled = play.disabled = timeline.delays.length < 2;
        now.hidden = !Number.isInteger(timeline.latestObservation);
        show(timeline.delays.length - 1);
      } catch (error) {
        console.warn("Radar timeline", error);
        nationalFailed = true;
        fallback.src = "/radar.gif";
        note.textContent = "Timeline unavailable; showing the original animation.";
      }
      present();
    })();
    return nationalLoad;
  }

  async function loadLocal() {
    const query = new URLSearchParams(player.dataset.location);
    const params = new URLSearchParams();
    ["lat", "lon", "name", "label"].forEach(function (key) {
      if (query.has(key)) params.set(key, query.get(key));
    });
    const controller = new AbortController();
    const timeout = setTimeout(function () { controller.abort(); }, 15000);
    try {
      const response = await fetch("/api/v1/radar/local?" + params.toString(), {signal: controller.signal});
      if (response.status === 204) { localButton.title = "No fresh nearby rain"; return; }
      if (!response.ok) throw new Error("Local radar: HTTP " + response.status);
      const age = Date.now() - new Date(response.headers.get("X-Radar-Observed-At")).getTime();
      if (!Number.isFinite(age) || age < -120000 || age > 1200000) {
        localButton.title = "Local observation is unavailable or stale";
        return;
      }
      const blobURL = URL.createObjectURL(await response.blob());
      local.src = blobURL;
      try { await local.decode(); } finally { URL.revokeObjectURL(blobURL); }
      localReady = true;
      localButton.disabled = false;
      localButton.removeAttribute("title");
      if (!chosen) select("local");
      if (window.location.hash === "#local-radar") local.scrollIntoView();
    } catch (error) {
      localButton.title = "Local radar unavailable";
      console.debug("Local radar unavailable", error);
    } finally {
      clearTimeout(timeout);
      if (!localReady && !chosen) select("national");
    }
  }

  function load() { loadLocal(); }
  if ("IntersectionObserver" in window) {
    const observer = new IntersectionObserver(function (entries) {
      if (entries.some(function (entry) { return entry.isIntersecting; })) { observer.disconnect(); load(); }
    }, {rootMargin: "200px"});
    observer.observe(player);
  } else { load(); }
})();
