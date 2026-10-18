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
  const progress = controls.querySelector("[data-progress]");
  const note = player.querySelector(".radar-note");
  const credit = player.querySelector(".radar-credit");
  const localButton = player.querySelector('[data-view="local"]');
  const scheme = window.matchMedia("(prefers-color-scheme: dark)");
  const themedStates = {
    light: {local: {frame: 0}, national: {frame: 0}},
    dark: {local: {frame: 0}, national: {frame: 0}}
  };
  let theme = scheme.matches ? "dark" : "light";
  let states = themedStates[theme];
  const widgetPlayback = new URLSearchParams(window.location.search).get("radar") === "play";
  let mode = "national";
  let chosen = false;
  let localReady = false;
  let timer = null;
  let pendingPlay = false;
  let playRequest = 0;

  function locationParams() {
    const query = new URLSearchParams(player.dataset.location);
    const params = new URLSearchParams();
    ["lat", "lon", "name", "label"].forEach(function (key) {
      if (query.has(key)) params.set(key, query.get(key));
    });
    params.set("theme", theme);
    return params;
  }

  function pause() {
    playRequest++;
    pendingPlay = false;
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
    help.hidden = !state.timeline || mode === "local";
    help.textContent = mode === "local" ? "Now is the latest observation; later frames are forecasts. Playback stops after one hour." : "Earlier ← → Later · Now selects the latest observation. Timestamp shown on the map; later frames may be forecasts.";
    credit.hidden = mode !== "local" || !localReady;
    note.hidden = !state.failed;
    note.textContent = mode === "local" ? "Forecast unavailable; showing the current observation." : "Timeline unavailable; showing the original animation.";
    play.disabled = (!!state.loading && !canPlay(state) && !pendingPlay) || !!state.failed || (!!state.timeline && state.timeline.delays.length < 2);
    play.textContent = timer !== null || pendingPlay ? "Pause" : mode === "local" && state.timeline && state.frame === state.timeline.delays.length - 1 ? "Replay" : "Play";
    play.setAttribute("aria-pressed", String(timer !== null || pendingPlay));
    progress.hidden = mode !== "local" || !state.loading;
    progress.textContent = state.metadata ? "Loading " + (state.frames || []).length + " / " + state.metadata.delays.length + " frames" : "Loading forecast…";
    latest.textContent = mode === "local" ? "+1h" : "Latest";
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
    const context = canvas.getContext("2d");
    if (state.frames) {
      context.drawImage(state.frames[index], 0, 0, canvas.width, canvas.height);
    } else {
      const x = (index % timeline.columns) * timeline.width;
      const y = Math.floor(index / timeline.columns) * timeline.height;
      context.drawImage(state.atlas, x, y, timeline.width, timeline.height, 0, 0, canvas.width, canvas.height);
    }
    canvas.setAttribute("aria-label", mode === "local" ? "Rain within 50 km of your location" : "KNMI precipitation and lightning radar over the Netherlands");
    slider.max = timeline.delays.length - 1;
    slider.value = index;
    slider.disabled = timeline.delays.length < 2;
    if (mode === "local") {
      const clock = new Date(timeline.times[index]).toLocaleTimeString([], {hour: "2-digit", minute: "2-digit", timeZone: player.dataset.timezone || "Europe/Amsterdam"});
      const minutes = Math.round((new Date(timeline.times[index]) - new Date(timeline.times[0])) / 60000);
      output.textContent = (index === 0 ? "Now" : "+" + minutes + " min") + " · " + clock;
    } else output.textContent = (index + 1) + " / " + timeline.delays.length;
    slider.setAttribute("aria-valuetext", mode === "local" ? output.textContent : "Frame " + (index + 1) + " of " + timeline.delays.length);
    latest.disabled = (state.frames && !state.complete) || index === timeline.delays.length - 1;
    now.disabled = index === timeline.latestObservation;
  }

  function advance() {
    const state = states[mode];
    if (state.frame === state.timeline.delays.length - 1) { pause(); return; }
    if (state.frames && !state.frames[state.frame + 1]) {
      timer = setTimeout(advance, 150);
      return;
    }
    show(state.frame + 1);
    if (state.frame === state.timeline.delays.length - 1) { pause(); return; }
    timer = setTimeout(advance, state.timeline.delays[state.frame]);
  }

  async function start() {
    if (timer !== null || pendingPlay) { pause(); present(); return; }
    const view = mode;
    const state = states[view];
    delete state.desiredFrame;
    const request = ++playRequest;
    pendingPlay = true;
    if (!canPlay(state)) await loadTimeline(view);
    if (request !== playRequest || mode !== view || !canPlay(state) || state.failed || document.hidden) return;
    pendingPlay = false;
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
  slider.addEventListener("input", function () {
    pause();
    const state = states[mode];
    delete state.desiredFrame;
    show(state.frames ? Math.min(Number(slider.value), state.frames.length - 1) : Number(slider.value));
    pause();
  });
  latest.addEventListener("click", function () { pause(); show(states[mode].timeline.delays.length - 1); pause(); });
  now.addEventListener("click", function () { pause(); delete states[mode].desiredFrame; show(states[mode].timeline.latestObservation); pause(); });
  document.addEventListener("visibilitychange", function () { if (document.hidden) pause(); });
  window.addEventListener("pagehide", pause);

  function loadTimeline(view) {
    const state = states[view];
    if (state.load) return view === "local" ? state.ready : state.load;
    let ready;
    if (view === "local") state.ready = new Promise(function (resolve) { ready = resolve; });
    state.loading = true;
    present();
    state.load = (async function () {
      const controller = new AbortController();
      const timeout = setTimeout(function () { controller.abort(); }, view === "local" ? 50000 : 15000);
      try {
        const params = locationParams();
        params.set("progressive", "1");
        const path = view === "local" ? "/api/v1/radar/local/timeline?" + params.toString() : "/api/v1/radar?theme=" + theme;
        const response = await fetch(path, {cache: "no-store", signal: controller.signal});
        if (!response.ok || response.status === 204) throw new Error("Radar timeline: HTTP " + response.status);
        if (view === "local" && (response.headers.get("Content-Type") || "").includes("application/x-ndjson")) {
          await readLocalStream(response, state, ready);
          return;
        }
        const candidate = await response.json();
        if (!Array.isArray(candidate.delays) || !candidate.delays.length) throw new Error("Radar timeline has no frames");
        if (view === "local") {
          validateLocal(candidate);
        }
        const responseAtlas = await fetch(candidate.atlasURL, {signal: controller.signal});
        if (!responseAtlas.ok) throw new Error("Radar frames: HTTP " + responseAtlas.status);
        const blobURL = URL.createObjectURL(await responseAtlas.blob());
        const image = new Image();
        image.src = blobURL;
        try { await image.decode(); } finally { URL.revokeObjectURL(blobURL); }
        state.timeline = candidate;
        state.atlas = image;
        state.frame = state.desiredFrame === undefined ? view === "local" ? 0 : candidate.delays.length - 1 : Math.min(state.desiredFrame, candidate.delays.length - 1);
        delete state.desiredFrame;
        state.complete = true;
        state.loadedAt = Date.now();
        if (ready) ready();
      } catch (error) {
        console.warn("Radar timeline", error);
        state.failed = true;
        if (view === "local") {
          state.timeline = null;
          state.frames = null;
          if (mode === view && state === states[view]) pause();
        }
        if (view === "national") {
          state.fallbackURL = "/radar.gif?theme=" + (state === themedStates.dark.national ? "dark" : "light");
          if (state === states.national) fallback.src = state.fallbackURL;
        }
      } finally {
        clearTimeout(timeout);
        state.loading = false;
        if (ready) ready();
        present();
      }
    })();
    return view === "local" ? state.ready : state.load;
  }

  function canPlay(state) {
    return !!state.timeline && (!state.frames || state.frames.length >= 3 || state.complete);
  }

  function validateLocal(candidate) {
    if (candidate.width !== 256 || candidate.height !== 256 || !Array.isArray(candidate.times) ||
        !Array.isArray(candidate.delays) || candidate.delays.length < 2 || candidate.delays.length > 13 ||
        candidate.times.length !== candidate.delays.length || candidate.latestObservation !== 0 ||
        !fresh(new Date(candidate.times[0]).getTime()) || !fresh(new Date(candidate.reference).getTime())) {
      throw new Error("Radar forecast is stale or incomplete");
    }
    candidate.times.forEach(function (at, index) {
      const instant = new Date(at).getTime();
      if (!Number.isFinite(instant) || instant - new Date(candidate.times[0]) > 3600000 ||
          (index > 0 && instant <= new Date(candidate.times[index - 1]).getTime()) ||
          !Number.isFinite(candidate.delays[index]) || candidate.delays[index] < 100) {
        throw new Error("Invalid radar frame timing");
      }
    });
  }

  async function readLocalStream(response, state, ready) {
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let pending = "";
    let complete = false;
    async function event(line) {
      const item = JSON.parse(line);
      if (complete) throw new Error("Unexpected data after radar completion");
      if (item.type === "metadata") {
        if (state.metadata) throw new Error("Repeated radar metadata");
        validateLocal(item);
        state.metadata = item;
        state.frames = [];
      } else if (item.type === "frame") {
        if (!state.metadata || item.index !== state.frames.length || item.index >= state.metadata.times.length ||
            typeof item.image !== "string" || !item.image.startsWith("data:image/png;base64,")) throw new Error("Invalid radar frame");
        const image = new Image();
        image.src = item.image;
        await image.decode();
        if (image.naturalWidth !== 256 || image.naturalHeight !== 256) throw new Error("Invalid radar frame dimensions");
        state.frames.push(image);
        state.timeline = state.metadata;
        if (state.desiredFrame !== undefined && state.frames.length > state.desiredFrame) {
          state.frame = state.desiredFrame;
          delete state.desiredFrame;
        }
        if (canPlay(state)) ready();
      } else if (item.type === "complete") {
        if (!state.metadata || state.frames.length !== state.metadata.times.length) throw new Error("Incomplete radar frames");
        complete = state.complete = true;
        ready();
      } else throw new Error("Radar forecast unavailable");
      present();
    }
    try {
      while (true) {
        const chunk = await reader.read();
        pending += decoder.decode(chunk.value || new Uint8Array(), {stream: !chunk.done});
        if (pending.length > 2000000) throw new Error("Radar stream exceeds frame budget");
        let end;
        while ((end = pending.indexOf("\n")) >= 0) {
          const line = pending.slice(0, end);
          pending = pending.slice(end + 1);
          if (line.trim()) await event(line);
        }
        if (chunk.done) break;
      }
      if (pending.trim()) await event(pending);
      if (!complete) throw new Error("Radar stream interrupted");
    } catch (error) {
      try { await reader.cancel(); } catch (cancelError) { console.debug("Cancel radar stream", cancelError); }
      throw error;
    }
  }

  function fresh(at) {
    const age = Date.now() - at;
    return Number.isFinite(age) && age >= -120000 && age <= 1200000;
  }

  async function loadLocal() {
    const requestTheme = theme;
    const state = states.local;
    const controller = new AbortController();
    const timeout = setTimeout(function () { controller.abort(); }, 15000);
    try {
      if (!state.observation || !fresh(state.observation.at)) {
        if (!state.observationLoad) state.observationLoad = (async function () {
          const preload = window.__radarObservation;
          window.__radarObservation = null;
          const response = preload && preload.theme === requestTheme ? await preload.response :
            await fetch("/api/v1/radar/local?" + locationParams().toString(), {signal: controller.signal});
          if (response.status === 204) {
            if (theme === requestTheme) localButton.title = "No fresh nearby rain";
            return;
          }
          if (!response.ok) throw new Error("Local radar: HTTP " + response.status);
          const at = new Date(response.headers.get("X-Radar-Observed-At")).getTime();
          if (!fresh(at)) {
            if (theme === requestTheme) localButton.title = "Local observation is unavailable or stale";
            return;
          }
          const url = URL.createObjectURL(await response.blob());
          const image = new Image();
          image.src = url;
          try { await image.decode(); } catch (error) { URL.revokeObjectURL(url); throw error; }
          if (state.observation) URL.revokeObjectURL(state.observation.url);
          state.observation = {url: url, at: at};
        })().finally(function () { state.observationLoad = null; });
        await state.observationLoad;
        if (!state.observation || !fresh(state.observation.at)) return;
      }
      if (theme !== requestTheme || state !== states.local) return;
      local.src = state.observation.url;
      await local.decode();
      if (theme !== requestTheme || state !== states.local) return;
      localReady = true;
      localButton.disabled = false;
      localButton.removeAttribute("title");
      if (!chosen) select("local");
      else present();
      if (window.location.hash === "#local-radar" || widgetPlayback) player.scrollIntoView();
      if (widgetPlayback && !chosen) start();
    } catch (error) {
      if (theme === requestTheme) localButton.title = "Local radar unavailable";
      console.debug("Local radar unavailable", error);
    } finally {
      clearTimeout(timeout);
      if (theme === requestTheme && !localReady && (!chosen || mode === "local")) select("national");
    }
  }

  scheme.addEventListener("change", function () {
    const previous = states[mode];
    pause();
    theme = scheme.matches ? "dark" : "light";
    states = themedStates[theme];
    if (states.local.timeline && (!fresh(new Date(states.local.timeline.times[0]).getTime()) ||
        !fresh(new Date(states.local.timeline.reference).getTime()))) {
      states.local = {frame: 0, observation: states.local.observation};
    }
    if (states.national.loadedAt && Date.now() - states.national.loadedAt > 360000) states.national = {frame: 0};
    chosen = true;
    localReady = false;
    localButton.disabled = true;
    local.hidden = canvas.hidden = fallback.hidden = true;
    if (previous.timeline) {
      const state = states[mode];
      if (state.timeline) state.frame = Math.min(previous.frame, state.frames ? state.frames.length - 1 : state.timeline.delays.length - 1);
      else state.desiredFrame = previous.frame;
    }
    if (states.national.fallbackURL) fallback.src = states.national.fallbackURL;
    const current = states;
    present();
    loadLocal().then(function () {
      if (states === current && mode === "local" && localReady && previous.timeline) loadTimeline("local");
    });
    if (mode === "national") loadTimeline("national");
  });

  function load() { loadLocal(); }
  if ("IntersectionObserver" in window) {
    const observer = new IntersectionObserver(function (entries) {
      if (entries.some(function (entry) { return entry.isIntersecting; })) { observer.disconnect(); load(); }
    }, {rootMargin: "200px"});
    observer.observe(player);
  } else { load(); }
})();
