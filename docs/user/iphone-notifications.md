# Brygge på iPhone

> Audience: members, and board members such as the harbour master, who want Brygge's push notifications on an iPhone or iPad.

An iPhone or iPad shows Brygge's notifications only when Brygge is on the Home Screen and opened from that icon. In a Safari tab the page cannot turn notifications on at all. It needs iOS or iPadOS 16.4 or newer (Settings → General → About).

## Before you start: a passkey

The Home Screen app keeps its own login, apart from Safari's. A sign-in link from Mail always opens in Safari, so it signs Safari in and never the app. Inside the app you sign in with a **passkey** (passnøkkel), which iCloud Keychain keeps on the phone:

- If you already have a passkey for Brygge (board members who use the admin pages usually do), skip ahead.
- Otherwise, sign in in Safari first, open **Sikkerhet** (Nynorsk: **Tryggleik**) in the portal menu, tap **Legg til passnøkkel** and save it in iCloud Keychain.

Members who cannot use a passkey cannot yet sign in to the Home Screen app; a typed login code is planned separately.

## Set it up

1. Open the club's Brygge site in **Safari**.
2. Tap the **Del** button (Share: the square with an arrow pointing up). On an iPhone it is at the bottom of the screen, on an iPad at the top.
3. Scroll down and choose **Legg til på Hjem-skjerm** (English: **Add to Home Screen**), then tap **Legg til**.
4. Close Safari and open Brygge from the **new icon** on the Home Screen. It opens without Safari's address bar.
5. Sign in with **Logg inn med passnøkkel** and confirm with Face ID or Touch ID. Do not use the email link here: it opens Safari, not the app.
6. Tap the bell at the top of the page to open **Varsler** (Nynorsk: **Varsel**).
7. Tap **Aktiver** and choose **Tillat** when the phone asks whether Brygge may send notifications.
8. Tap **Send testvarsel**. Within a few seconds the phone should show **Testvarsel fra** followed by the club's name, in your chosen language (Nynorsk: **Testvarsel frå …**). Tapping it opens the notifications page.

While you are on the page in a Safari tab, it shows these steps in a yellow box. The box disappears once you open Brygge from the Home Screen icon.

## Reading the test result

After **Send testvarsel** the page says how many of your devices the notification was sent to.

- **Varsler er blokkert for Brygge** (shown when you tap **Aktiver**): you, or an earlier prompt, said no. Turn **Allow Notifications** on under **Settings → Notifications → Brygge** and tap **Aktiver** again.
- **Sent to 1 of 1 device**, but nothing arrives: check **Settings → Notifications → Brygge** on the phone and make sure **Allow Notifications** is on. Focus modes can also hold notifications back.
- **Expired devices removed**: one of your devices had an old registration that the phone no longer accepts, and Brygge deleted it. On the device that did not get the notification, open Brygge from the Home Screen icon, go to **Varsler**, turn notifications off and on again.
- **No device is registered for notifications**: notifications were never turned on for this login, or the registration expired. Turn them on (step 7).
- **Push notifications are not set up on this server**: the club's server has no push keys. Tell the board or whoever runs the site.

You can send at most five test notifications a minute.

## Good to know

- Each device is registered separately. Turn notifications on once on every phone, iPad or computer that should get them, and use **Send testvarsel** to check all of them at once.
- Removing the Brygge icon from the Home Screen also removes its notifications. Add it again and repeat the steps.
- Which kinds of notifications you get is set in the list below the push switch on the same page; some of them the board has made required.
