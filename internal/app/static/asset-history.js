(() => {
  const form = document.querySelector('[data-history-defaults="true"]');
  if (!form) return;
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  const timezone = form.elements.event_timezone;
  const date = form.elements.occurred_on;
  // A delayed script must preserve values already entered into the form.
  if (zone && timezone.value === timezone.defaultValue) timezone.value = zone;
  const now = new Date();
  const pad = value => String(value).padStart(2, '0');
  if (date.value === date.defaultValue) {
    date.value = `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`;
  }
})();
