# PWA Web Push Notifications

## Overview

Service worker-based web push notifications for all key app events. Users configure
notification preferences in their profile. Admins can mark certain categories as
required (non-dismissable by users).

## Notification Categories

| Category             | Default | Can be required? | Trigger                                    |
|----------------------|---------|------------------|--------------------------------------------|
| `payment_reminder`   | on      | yes              | Invoice due date approaching / overdue      |
| `slip_offer`         | on      | yes              | Waiting list offer received                 |
| `booking_confirm`    | on      | no               | Booking confirmed or cancelled              |
| `dugnad_reminder`    | on      | yes              | Upcoming dugnad event (configurable days)   |
| `styre_announcement` | on      | yes              | Broadcast from styre/admin                  |
| `waiting_list`       | on      | no               | Position change or status update            |
| `new_document`       | off     | no               | New document uploaded                       |
| `event_reminder`     | on      | no               | Upcoming calendar event (24h before)        |

## Technical Approach

- **W3C Push API** + service worker (`sw.js` registered by Vue app)
- **VAPID keys** generated per deployment, stored in server config
- Backend sends push via `web-push` library (Go: `github.com/SherClockHolmes/webpush-go`)
- Subscriptions stored in `push_subscriptions` table (user_id, endpoint, p256dh, auth, created_at)
- Subscription created on user opt-in; removed on logout or manual unsubscribe

## iOS Considerations

- Web push works on iOS 16.4+ but only when PWA is added to home screen
- Show a prompt/banner for iOS users explaining how to enable notifications
- Detect `navigator.standalone` to know if running as installed PWA

## User Profile — Notification Preferences

- Toggle per category in profile settings
- Required categories: toggle is visible but disabled (grayed out) with "(obligatorisk)" label
- "Test notification" button to verify push is working

## Admin — Notification Configuration

- Admin panel page under communication or settings
- Per-category: toggle "required" flag
- Dugnad reminder: configurable lead time (1 day, 3 days, 1 week)
- Preview/test send to self

## Schema

```sql
CREATE TABLE push_subscriptions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    club_id     UUID NOT NULL REFERENCES clubs(id),
    endpoint    TEXT NOT NULL,
    p256dh      TEXT NOT NULL,
    auth        TEXT NOT NULL,
    user_agent  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_push_sub_endpoint ON push_subscriptions(endpoint);

CREATE TABLE notification_preferences (
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    club_id     UUID NOT NULL REFERENCES clubs(id),
    category    TEXT NOT NULL,
    enabled     BOOLEAN NOT NULL DEFAULT true,
    PRIMARY KEY (user_id, club_id, category)
);

CREATE TABLE notification_config (
    club_id     UUID NOT NULL REFERENCES clubs(id),
    category    TEXT NOT NULL,
    required    BOOLEAN NOT NULL DEFAULT false,
    lead_days   INT,
    PRIMARY KEY (club_id, category)
);
```

## API Endpoints

- `POST /api/v1/push/subscribe` — register push subscription
- `DELETE /api/v1/push/subscribe` — unsubscribe
- `GET /api/v1/members/me/notifications` — get preferences
- `PUT /api/v1/members/me/notifications` — update preferences
- `GET /api/v1/admin/notifications/config` — get club notification config
- `PUT /api/v1/admin/notifications/config` — update config (set required, lead days)
- `POST /api/v1/admin/notifications/test` — send test push to self

## Future

- Capacitor/TWA native wrapper for App Store/Google Play distribution
  (documented in TODO.md — requires Apple Developer + Google Play accounts)
