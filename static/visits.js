document.getElementById("select-all")?.addEventListener("change", function (event) {
  document.querySelectorAll(".visit-check").forEach((checkbox) => {
    checkbox.checked = event.target.checked;
  });
});
