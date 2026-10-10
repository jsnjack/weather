document.addEventListener("input", function (event) {
  const input = event.target;
  const form = input.form;
  if (!form || !form.classList.contains("controls")) return;
  if (input.name === "name") {
    form.elements.lat.value = "";
    form.elements.lon.value = "";
    form.elements.label.value = "";
  } else if (input.name === "lat" || input.name === "lon") {
    form.elements.name.value = "";
    form.elements.label.value = "";
  }
});
