// Cross-component unread-count event bus (feature #26, AC9/D5).
//
// The shell NotificationBell and the notifications page are separate
// components in the same surface: the bell lives in the shell header and
// the page is rendered as the shell's children. When the page marks
// notifications read (MarkNotificationRead / MarkAllNotificationsRead) it
// must tell the bell to re-poll GetUnreadCount immediately instead of
// waiting for the 30s poll or a page reload. This module-level emitter
// lets the page notify the bell without prop drilling or a React context.
//
// Only one surface (admin or user) is mounted at a time, so a single
// module-level listener set is safe: an emit from a page only ever reaches
// the bell of the currently mounted surface.

type Listener = () => void;

const listeners = new Set<Listener>();

/** Subscribe to unread-count-change notifications; returns an unsubscribe fn. */
export function subscribeUnreadChanged(listener: Listener): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

/** Notify subscribers (the shell bell) that the unread count may have changed. */
export function notifyUnreadChanged(): void {
  listeners.forEach((listener) => listener());
}