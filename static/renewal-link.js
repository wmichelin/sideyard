const renewalLinkForm = document.querySelector("[data-renewal-link-form]");

renewalLinkForm?.addEventListener("submit", async (event) => {
  event.preventDefault();

  const button = renewalLinkForm.querySelector("button[type=submit]");
  const result = document.querySelector("[data-renewal-link-result]");
  const link = document.querySelector("[data-renewal-link]");
  const status = document.querySelector("[data-renewal-link-status]");
  button.disabled = true;
  status.hidden = true;

  try {
    const response = await fetch(renewalLinkForm.action, {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/x-www-form-urlencoded",
      },
      body: new URLSearchParams(new FormData(renewalLinkForm)),
      credentials: "same-origin",
    });
    if (!response.ok) {
      throw new Error("create renewal link failed");
    }

    const { renewal_url: renewalURL } = await response.json();
    if (!renewalURL) {
      throw new Error("renewal link response was empty");
    }
    link.href = renewalURL;
    link.textContent = renewalURL;
    result.hidden = false;
    status.textContent = "Renewal link created.";
    status.hidden = false;
  } catch {
    status.textContent = "Could not create the renewal link. Please try again.";
    status.hidden = false;
  } finally {
    button.disabled = false;
  }
});
