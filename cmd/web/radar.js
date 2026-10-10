(function () {
  const player = document.getElementById("radar-player");
  if (!player) return;
  const fallback = player.querySelector("img");
  const canvas = player.querySelector("canvas");
  const controls = player.querySelector(".radar-controls");
  const slider = controls.querySelector("input");
  const play = controls.querySelector("[data-play]");
  const latest = controls.querySelector("[data-latest]");
  const now = controls.querySelector("[data-now]");
  const output = controls.querySelector("output");
  let timer = null;
  let frame = 0;
  let timeline;
  let atlas;
  fallback.style.visibility = "hidden";

  function pause() {
    clearTimeout(timer);
    timer = null;
    play.textContent = "Play";
    play.setAttribute("aria-pressed", "false");
  }

  function show(index) {
    frame = index;
    const x = (index % timeline.columns) * timeline.width;
    const y = Math.floor(index / timeline.columns) * timeline.height;
    const context = canvas.getContext("2d");
    context.clearRect(0, 0, canvas.width, canvas.height);
    context.drawImage(atlas, x, y, timeline.width, timeline.height, 0, 0, canvas.width, canvas.height);
    slider.value = index;
    const label = "Frame " + (index + 1) + " of " + timeline.delays.length;
    output.textContent = (index + 1) + " / " + timeline.delays.length;
    slider.setAttribute("aria-valuetext", label);
    latest.disabled = index === timeline.delays.length - 1;
    now.disabled = index === timeline.latestObservation;
  }

  function advance() {
    show(frame + 1);
    if (frame === timeline.delays.length - 1) { pause(); return; }
    timer = setTimeout(advance, timeline.delays[frame]);
  }

  play.addEventListener("click", function () {
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

  async function load() {
    try {
      const response = await fetch("/api/v1/radar", {cache: "no-store"});
      if (!response.ok) throw new Error("Radar timeline: HTTP " + response.status);
      timeline = await response.json();
      if (!timeline.delays.length) throw new Error("Radar timeline has no frames");
      atlas = new Image();
      atlas.src = timeline.atlasURL;
      await atlas.decode();
      canvas.width = timeline.width;
      canvas.height = timeline.height;
      slider.max = timeline.delays.length - 1;
      slider.disabled = play.disabled = timeline.delays.length < 2;
      now.hidden = !Number.isInteger(timeline.latestObservation);
      show(timeline.delays.length - 1);
      fallback.hidden = true;
      fallback.removeAttribute("src");
      canvas.hidden = controls.hidden = false;
    } catch (error) {
      console.warn("Radar timeline", error);
      fallback.style.visibility = "";
      const note = player.querySelector(".radar-note");
      note.textContent = "Timeline unavailable; showing the original animation.";
      note.hidden = false;
    }
  }

  if ("IntersectionObserver" in window) {
    const observer = new IntersectionObserver(function (entries) {
      if (entries.some(function (entry) { return entry.isIntersecting; })) { observer.disconnect(); load(); }
    }, {rootMargin: "200px"});
    observer.observe(player);
  } else { load(); }
})();
