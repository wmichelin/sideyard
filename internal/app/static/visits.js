const workDialog = document.getElementById("work-confirmation");

function selectionScope(form, checked) {
  const rows = checked.map((box) => box.closest("[data-work-row]"));
  if (form.dataset.workForm === "seasonal") {
    const windows = [...new Set(rows.map((row) => row.dataset.workWindow))];
    return "Seasonal windows: " + windows.join("; ") + ".";
  }
  const dated = rows.slice().sort((a, b) => a.dataset.workDate.localeCompare(b.dataset.workDate));
  const first = dated[0].dataset.workDateLabel;
  const last = dated[dated.length - 1].dataset.workDateLabel;
  const future = rows.some((row) => row.dataset.workUpcoming === "true");
  return "Due " + first + (first === last ? "" : " – " + last) + "." +
    (future ? " This selection includes upcoming work." : "");
}

function workFeedback(form, message, error = false) {
  const feedback = form.querySelector("[data-work-feedback]");
  feedback.dataset.error = String(error);
  feedback.textContent = message;
  feedback.hidden = false;
  feedback.focus({ preventScroll: true });
}

function initializeWorkForm(form) {
  const boxes = [...form.querySelectorAll(".service-check, .visit-check")];
  const master = form.querySelector("[data-select-all]");
  const clear = form.querySelector("[data-clear-selection]");
  const count = form.querySelector("[data-selection-count]");
  const actions = [...form.querySelectorAll("button[type=submit]")];
  let saving = false;

  function updateSelection() {
    const selected = boxes.filter((box) => box.checked);
    count.hidden = false;
    count.textContent = selected.length + " selected";
    master.checked = boxes.length > 0 && selected.length === boxes.length;
    master.indeterminate = selected.length > 0 && selected.length < boxes.length;
    master.disabled = saving || boxes.length === 0;
    clear.hidden = selected.length === 0;
    clear.disabled = saving;
    actions.forEach((button) => { button.disabled = saving || selected.length === 0; });
    boxes.forEach((box) => { box.closest("[data-work-row]").dataset.selected = String(box.checked); });
  }
  boxes.forEach((box) => box.addEventListener("change", updateSelection));
  master.addEventListener("change", () => {
    boxes.forEach((box) => { box.checked = master.checked; });
    updateSelection();
  });
  clear.addEventListener("click", () => {
    boxes.forEach((box) => { box.checked = false; });
    updateSelection();
    master.focus();
  });
  updateSelection();

  form.addEventListener("focusin", (event) => {
    if (!event.target.closest("[data-work-row]")) return;
    const toolbar = form.querySelector(".work-toolbar").getBoundingClientRect();
    const focused = event.target.getBoundingClientRect();
    if (toolbar.bottom > 0 && focused.top < toolbar.bottom + 8) {
      window.scrollBy({ top: focused.top - toolbar.bottom - 8, behavior: "instant" });
    }
  });

  if (!workDialog || typeof workDialog.showModal !== "function") return;

  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const checked = boxes.filter((box) => box.checked);
    const button = event.submitter;
    if (saving || checked.length === 0 || !button) return;
    const action = button.value;
    const outcome = action === "scheduled" ? "handed off" : action;
    const label = checked.length === 1 ? form.dataset.workLabel.slice(0, -1) : form.dataset.workLabel;
    const title = "Mark " + checked.length + " " + label + " " + outcome + "?";
    const scope = selectionScope(form, checked);
    workDialog.querySelector("h3").textContent = title;
    workDialog.querySelector("#work-confirmation-scope").textContent = scope;
    workDialog.querySelector("[data-confirm-save]").textContent = "Mark " + outcome;
    const confirmed = await new Promise((resolve) => {
      workDialog.returnValue = "";
      workDialog.addEventListener("close", () => resolve(workDialog.returnValue === "save"), { once: true });
      workDialog.querySelector("[data-confirm-cancel]").onclick = () => workDialog.close("cancel");
      workDialog.querySelector("[data-confirm-save]").onclick = () => workDialog.close("save");
      workDialog.showModal();
    });
    if (!confirmed) {
      button.focus({ preventScroll: true });
      return;
    }

    const body = new URLSearchParams(new FormData(form));
    body.set("action", action);
    saving = true;
    boxes.forEach((box) => { box.disabled = true; });
    updateSelection();
    form.setAttribute("aria-busy", "true");
    workFeedback(form, "Saving selected work…");
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 110000);

    try {
      // Named action buttons shadow HTMLFormElement.action in some browsers.
      const response = await fetch(form.getAttribute("action"), {
        method: "POST",
        headers: { Accept: "application/json", "Content-Type": "application/x-www-form-urlencoded" },
        body,
        credentials: "same-origin",
        signal: controller.signal,
      });
      if (response.redirected || response.status === 401) {
        throw new Error("Your session ended. Sign in again in another tab, then retry this selection.");
      }
      const json = response.headers.get("Content-Type")?.includes("application/json");
      const result = json ? await response.json() : null;
      if (!response.ok) {
        if (result?.error) throw new Error(result.error);
        if (response.status === 403) throw new Error("This page can no longer save changes. Reload the page before trying again; your selection is still shown here.");
        if (response.status === 413) throw new Error("This selection is too large. Select fewer visits and try again.");
        if (response.status === 429) throw new Error("The request limit was reached. Wait before retrying; if this demo is full, start a fresh demo.");
        throw new Error("The save could not be confirmed. Your selection is kept; check your connection and retry.");
      }
      if (!result?.saved) throw new Error("The save could not be confirmed. Your selection is kept; retry when ready.");
      if (!result.html) {
        workFeedback(form, result.message + " Reload this page to see the updated list.");
        boxes.forEach((box) => { box.checked = false; });
        return;
      }
      const fragment = document.createElement("template");
      fragment.innerHTML = result.html;
      const replacement = fragment.content.querySelector("[data-work-form]");
      if (!replacement || replacement.dataset.workForm !== form.dataset.workForm) {
        workFeedback(form, result.message + " Reload this page to see the updated list.");
        boxes.forEach((box) => { box.checked = false; });
        return;
      }
      form.replaceWith(replacement);
      initializeWorkForm(replacement);
      workFeedback(replacement, result.message);
      replacement.querySelector(".work-toolbar").scrollIntoView({ block: "start", behavior: "instant" });
    } catch (error) {
      workFeedback(form, error.name !== "Error" ?
        "The save could not be confirmed. Your selection is kept; check your connection and retry." : error.message, true);
    } finally {
      clearTimeout(timeout);
      saving = false;
      if (form.isConnected) {
        boxes.forEach((box) => { box.disabled = false; });
        form.removeAttribute("aria-busy");
        updateSelection();
      }
    }
  });
}

document.querySelectorAll("[data-work-form]").forEach(initializeWorkForm);
