const renewalLinkForm = document.querySelector("[data-renewal-link-form]");

renewalLinkForm?.addEventListener("submit", async (event) => {
  event.preventDefault();

  const button = renewalLinkForm.querySelector("button[type=submit]");
  const result = document.querySelector("[data-renewal-link-result]");
  const link = document.querySelector("[data-renewal-link]");
  const status = document.querySelector("[data-renewal-link-status]");
  button.disabled = true;
  status.hidden = true;

  let failureMessage = "Could not confirm the renewal link. Reload the agreement before trying again.";

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
      if (response.status === 404) {
        failureMessage = "This agreement is no longer available. Reload the agreement list.";
      }
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
    status.textContent = failureMessage;
    status.hidden = false;
  } finally {
    button.disabled = false;
  }
});
