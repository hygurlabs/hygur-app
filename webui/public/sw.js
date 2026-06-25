// Hygur — Web Push service worker. Shows notifications when the tab is closed.
// Payload shape (sent by internal/push): { title, body, icon, data: { url } }.

self.addEventListener("push", (event) => {
  if (!event.data) return;
  let data = {};
  try {
    data = event.data.json();
  } catch {
    data = { title: "Hygur", body: event.data.text() };
  }
  const options = {
    body: data.body,
    icon: data.icon || "/icon-192.png",
    badge: "/icon-192.png",
    data: data.data || {},
    tag: (data.data && data.data.type) || "hygur",
    renotify: true,
  };
  event.waitUntil(
    (async () => {
      // De-dupe with the in-app (foreground) notification: if a Hygur window is
      // already focused/visible, the app notified itself — skip the push banner.
      // A test push (data.test) always shows so the user can verify the setup.
      if (!(data.data && data.data.test)) {
        const wins = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
        if (wins.some((c) => c.focused || c.visibilityState === "visible")) return;
      }
      await self.registration.showNotification(data.title || "Hygur", options);
    })(),
  );
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const url = (event.notification.data && event.notification.data.url) || "/";
  event.waitUntil(
    self.clients.matchAll({ type: "window", includeUncontrolled: true }).then((list) => {
      for (const client of list) {
        if (client.url.includes(self.location.origin) && "focus" in client) {
          client.focus();
          if ("navigate" in client) client.navigate(url);
          return;
        }
      }
      if (self.clients.openWindow) return self.clients.openWindow(url);
    }),
  );
});
