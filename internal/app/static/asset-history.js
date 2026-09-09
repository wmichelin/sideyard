(() => {
  const form = document.querySelector('[data-history-defaults="true"]');
  if (!form) return;
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  if (zone) form.elements.event_timezone.value = zone;
  const now = new Date();
  const pad = value => String(value).padStart(2, '0');
  form.elements.occurred_on.value = `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`;
})();
