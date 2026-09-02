document.getElementById("select-all")?.addEventListener("change", function (event) {
  document.querySelectorAll(".visit-check").forEach((checkbox) => {
    checkbox.checked = event.target.checked;
  });
});

document.getElementById("select-service-all")?.addEventListener("change", function (event) {
  document.querySelectorAll(".service-check").forEach((checkbox) => {
    checkbox.checked = event.target.checked;
  });
});
