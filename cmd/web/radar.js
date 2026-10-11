(function () {
  const player = document.getElementById("radar-player");
  if (!player) return;
  const local = player.querySelector("[data-local-map]");
  const fallback = player.querySelector("[data-national-fallback]");
  const canvas = player.querySelector("canvas");
  const controls = player.querySelector(".radar-controls");
  const sliderRow = controls.querySelector(".radar-slider");
  const slider = controls.querySelector("input");
  const play = controls.querySelector("[data-play]");
  const latest = controls.querySelector("[data-latest]");
  const now = controls.querySelector("[data-now]");
  const output = controls.querySelector("output");
  const help = controls.querySelector("[data-help]");
  const note = player.querySelector(".radar-note");
  const credit = player.querySelector(".radar-credit");
  const localButton = player.querySelector('[data-view="local"]');
  const states = {local: {frame: 0}, national: {frame: 0}};
  const widgetPlayback = new URLSearchParams(window.location.search).get("radar") === "play";
  let mode = "national";
  let chosen = false;
  let localReady = false;
  let timer = null;
  let playRequest = 0;

  function locationParams() {
    const query = new URLSearchParams(player.dataset.location);
    const params = new URLSearchParams();
    ["lat", "lon", "name", "label"].forEach(function (key) {
      if (query.has(key)) params.set(key, query.get(key));
    });
    return params;
  }

  function pause() {
    playRequest++;
    clearTimeout(timer);
    timer = null;
    const state = states[mode];
    play.textContent = mode === "local" && state.timeline && state.frame === state.timeline.delays.length - 1 ? "Replay" : "Play";
    play.setAttribute("aria-pressed", "false");
  }

  function present() {
    const state = states[mode];
    local.hidden = mode !== "local" || !localReady || !!state.timeline;
    canvas.hidden = !state.timeline;
    fallback.hidden = mode !== "national" || !state.failed;
    controls.hidden = !state.timeline && !(mode === "local" && localReady);
    sliderRow.hidden = !state.timeline;
    latest.hidden = !state.timeline;
    now.hidden = !state.timeline || !Number.isInteger(state.timeline.latestObservation);
    help.hidden = !state.timeline;
    help.textContent = mode === "local" ? "Now is the latest observation; later frames are forecasts. Playback stops after one hour." : "Earlier ← → Later · Now selects the latest observation. Timestamp shown on the map; later frames may be forecasts.";
    credit.hidden = mode !== "local" || !localReady;
    note.hidden = !state.failed;
    note.textContent = mode === "local" ? "Forecast unavailable; showing the current observation." : "Timeline unavailable; showing the original animation.";
    play.disabled = !!state.loading || !!state.failed || (!!state.timeline && state.timeline.delays.length < 2);
    if (!state.timeline) output.textContent = state.loading ? "Loading forecast…" : mode === "local" ? "Current observation" : "";
    player.querySelectorAll("[data-view]").forEach(function (button) {
      button.setAttribute("aria-pressed", String(button.dataset.view === mode));
    });
    if (state.timeline) show(state.frame);
  }

  function select(view) {
    pause();
    mode = view;
    pause();
    present();
    if (view === "national") loadTimeline("national");
  }

  function show(index) {
    const state = states[mode];
    const timeline = state.timeline;
    state.frame = index;
    canvas.width = timeline.width;
    canvas.height = timeline.height;
    const x = (index % timeline.columns) * timeline.width;
    const y = Math.floor(index / timeline.columns) * timeline.height;
    const context = canvas.getContext("2d");
    context.drawImage(state.atlas, x, y, timeline.width, timeline.height, 0, 0, canvas.width, canvas.height);
    canvas.setAttribute("aria-label", mode === "local" ? "Rain within 50 km of your location" : "KNMI precipitation and lightning radar over the Netherlands");
    slider.max = timeline.delays.length - 1;
    slider.value = index;
    slider.disabled = timeline.delays.length < 2;
    if (mode === "local") {
      const clock = new Date(timeline.times[index]).toLocaleTimeString([], {hour: "2-digit", minute: "2-digit"});
      const minutes = Math.round((new Date(timeline.times[index]) - new Date(timeline.times[0])) / 60000);
      output.textContent = (index === 0 ? "Now" : "+" + minutes + " min") + " · " + clock;
    } else output.textContent = (index + 1) + " / " + timeline.delays.length;
    slider.setAttribute("aria-valuetext", mode === "local" ? output.textContent : "Frame " + (index + 1) + " of " + timeline.delays.length);
    latest.disabled = index === timeline.delays.length - 1;
    now.disabled = index === timeline.latestObservation;
  }

  function advance() {
    const state = states[mode];
    show(state.frame + 1);
    if (state.frame === state.timeline.delays.length - 1) { pause(); return; }
    timer = setTimeout(advance, state.timeline.delays[state.frame]);
  }

  async function start() {
    if (timer !== null) { pause(); return; }
    const view = mode;
    const state = states[view];
    const request = ++playRequest;
    if (!state.timeline) await loadTimeline(view);
    if (request !== playRequest || mode !== view || !state.timeline || document.hidden) return;
    if (widgetPlayback) player.scrollIntoView({block: "start"});
    if (state.frame === state.timeline.delays.length - 1) show(0);
    play.textContent = "Pause";
    play.setAttribute("aria-pressed", "true");
    timer = setTimeout(advance, state.timeline.delays[state.frame]);
  }

  player.querySelectorAll("[data-view]").forEach(function (button) {
    button.addEventListener("click", function () { chosen = true; select(button.dataset.view); });
  });
  play.addEventListener("click", start);
  slider.addEventListener("input", function () { pause(); show(Number(slider.value)); pause(); });
  latest.addEventListener("click", function () { pause(); show(states[mode].timeline.delays.length - 1); pause(); });
  now.addEventListener("click", function () { pause(); show(states[mode].timeline.latestObservation); pause(); });
  document.addEventListener("visibilitychange", function () { if (document.hidden) pause(); });
  window.addEventListener("pagehide", pause);

  function loadTimeline(view) {
    const state = states[view];
    if (state.load) return state.load;
    state.loading = true;
    present();
    state.load = (async function () {
      const controller = new AbortController();
      const timeout = setTimeout(function () { controller.abort(); }, view === "local" ? 50000 : 15000);
      try {
        const path = view === "local" ? "/api/v1/radar/local/timeline?" + locationParams().toString() : "/api/v1/radar";
        const response = await fetch(path, {cache: "no-store", signal: controller.signal});
        if (!response.ok || response.status === 204) throw new Error("Radar timeline: HTTP " + response.status);
        const candidate = await response.json();
        if (!Array.isArray(candidate.delays) || !candidate.delays.length) throw new Error("Radar timeline has no frames");
        if (view === "local") {
          const observed = new Date(candidate.times && candidate.times[0]).getTime();
          const reference = new Date(candidate.reference).getTime();
          if (!fresh(observed) || !fresh(reference) || candidate.times.length !== candidate.delays.length) throw new Error("Radar forecast is stale or incomplete");
        }
        const responseAtlas = await fetch(candidate.atlasURL, {signal: controller.signal});
        if (!responseAtlas.ok) throw new Error("Radar frames: HTTP " + responseAtlas.status);
        const blobURL = URL.createObjectURL(await responseAtlas.blob());
        const image = new Image();
        image.src = blobURL;
        try { await image.decode(); } finally { URL.revokeObjectURL(blobURL); }
        state.timeline = candidate;
        state.atlas = image;
        state.frame = view === "local" ? 0 : candidate.delays.length - 1;
      } catch (error) {
        console.warn("Radar timeline", error);
        state.failed = true;
        if (view === "national") fallback.src = "/radar.gif";
      } finally {
        clearTimeout(timeout);
        state.loading = false;
        present();
      }
    })();
    return state.load;
  }

  function fresh(at) {
    const age = Date.now() - at;
    return Number.isFinite(age) && age >= -120000 && age <= 1200000;
  }

  async function loadLocal() {
    const controller = new AbortController();
    const timeout = setTimeout(function () { controller.abort(); }, 15000);
    try {
      const response = await fetch("/api/v1/radar/local?" + locationParams().toString(), {signal: controller.signal});
      if (response.status === 204) { localButton.title = "No fresh nearby rain"; return; }
      if (!response.ok) throw new Error("Local radar: HTTP " + response.status);
      if (!fresh(new Date(response.headers.get("X-Radar-Observed-At")).getTime())) {
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
      if (window.location.hash === "#local-radar" || widgetPlayback) player.scrollIntoView();
      if (widgetPlayback && !chosen) start();
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
