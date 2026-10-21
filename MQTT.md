# MQTT and Home Assistant

This fork can be controlled over MQTT, with entities created automatically in
Home Assistant (HA). You can:

- **Navigate** – go to the next or previous image.
- **Show a text message** – a banner on top of the slideshow for a set time
  ("Dinner is ready!").
- **Override the slideshow** – show one image or album from a pre-approved list
  for a set time (tonight's recipe in the kitchen).

Everything works for all screens at once, or for a single named screen.

- [Setup](#setup)
- [Naming screens](#naming-screens)
- [What appears in Home Assistant](#what-appears-in-home-assistant)
- [Navigation](#navigation)
- [Text messages](#text-messages)
- [Override](#override)
- [Home Assistant examples](#home-assistant-examples)
- [MQTT topic reference](#mqtt-topic-reference)
- [Good to know](#good-to-know)

## Setup

You need an MQTT broker (e.g. the Mosquitto add-on) and the
[MQTT integration](https://www.home-assistant.io/integrations/mqtt/) in Home
Assistant, with discovery enabled (the default).

Add this to the `kiosk:` section of `config.yaml`:

```yaml
kiosk:
  mqtt_enabled: true
  mqtt_broker: "192.168.1.100"     # IP or hostname of the broker
  mqtt_port: 1883
  mqtt_username: "kiosk"           # optional
  mqtt_password: "secret"          # optional
  mqtt_client_id: "immich-kiosk"   # must be unique per Kiosk instance
  mqtt_topic_prefix: "immich-kiosk"
```

Or use environment variables: `KIOSK_MQTT_ENABLED`, `KIOSK_MQTT_BROKER`,
`KIOSK_MQTT_PORT`, `KIOSK_MQTT_USERNAME`, `KIOSK_MQTT_PASSWORD`,
`KIOSK_MQTT_CLIENT_ID`, `KIOSK_MQTT_TOPIC_PREFIX`.

Restart Kiosk. The log shows `MQTT connected` and `MQTT enabled`, and an
**Immich Kiosk (all)** device appears in HA under Settings → Devices & services
→ MQTT.

Text messages are always available once MQTT is enabled. Override needs extra
configuration, see [Override](#override).

## Naming screens

To control one screen on its own, open Kiosk on that screen with a `client`
name in the URL:

```
http://kiosk:3000/?client=kitchen
```

- Allowed: letters, digits, `-` and `_`, up to 32 characters.
- Reserved: `override`, `message`, `_global`.
- An installed PWA keeps the `client` parameter, so it starts as the same screen.
- Screens without a name only follow commands sent to all screens.

When a named screen connects for the first time, HA gets a device called
**Immich Kiosk - kitchen**. Several browsers can share one name; they then act
as one screen.

## What appears in Home Assistant

Every device (**Immich Kiosk (all)** and each **Immich Kiosk - &lt;name&gt;**)
gets the same entities. Commands on the "all" device go to every screen.

| Entity | Type | Section | What it does |
|---|---|---|---|
| Image: next | button | Controls | Next image |
| Image: previous | button | Controls | Previous image |
| Message: send | button | Controls | Shows *Message: content* for *Message: duration* |
| Message: stop | button | Controls | Removes the message |
| Override: &lt;source name&gt; | select | Controls | One per override source: pick an image, or `off` |
| Message: content | text | Configuration | The message text (max 255 characters) |
| Message: duration | number | Configuration | Minutes the message stays (1–1440, default 5) |
| Override: duration | number | Configuration | Minutes an override stays (1 to `override_max_duration`) |

The override entities only exist when `override_sources` is configured.

HA sorts entities alphabetically by name within each section, which is why the
names have prefixes. The content and duration values are stored as retained
MQTT messages, so they survive a Kiosk restart.

## Navigation

Press **Image: next** / **Image: previous**, or publish `next` or `previous` to:

```
immich-kiosk/command            # all screens
immich-kiosk/kitchen/command    # only "kitchen"
```

## Text messages

A message is a banner on top of the slideshow. The slideshow keeps running
behind it, and the banner does not block taps.

**From the HA device page:** type the text in **Message: content**, set
**Message: duration**, and press **Message: send**. Press **Message: stop** to
remove it early.

**Rules:**

- A new message replaces the current one on that device.
- A message sent to one screen wins over a message sent to all screens. When it
  ends, that screen shows the "all" message again if it is still running.
- A message stays visible across a page reload, but not across a Kiosk restart.
- `\n` in the text gives a line break.
- HTML in the text is shown as plain text.

**From automations and scripts**, publish to `immich-kiosk/message/command` (all
screens) or `immich-kiosk/<name>/message/command`:

| Payload | Effect |
|---|---|
| `show` | Show the text from *Message: content* |
| `clear` | Remove the message |
| any other text | Show that text for *Message: duration* |
| JSON (see below) | Full control |

```json
{"text": "Dinner is ready!\nCome and eat", "duration": 10, "position": "center", "size": "large"}
```

| Field | Values | Default |
|---|---|---|
| `text` | the message; longer than 1000 characters is cut | required |
| `duration` | minutes, decimals allowed, up to 1440 | *Message: duration* |
| `position` | `bottom`, `top`, `center` | `bottom` |
| `size` | `small`, `medium`, `large` | `medium` |

A plain-text message of exactly `show` or `clear` is read as that command;
send it as JSON instead.

**Styling:** the banner uses the classes `.kiosk-message`,
`.kiosk-message--position-<position>` and `.kiosk-message--size-<size>`.
Override them in a `custom.css` file mounted into the container
(`./custom.css:/custom.css`), for example:

```css
.kiosk-message {
    background-color: rgba(30, 210, 187, 0.8);
    color: #000;
}
.kiosk-message--size-large {
    font-size: 6rem;
}
```

## Override

An override replaces the slideshow for a set time with one image, or a whole
album, picked from albums you have approved in the config. Afterwards the
screen goes back to its normal slideshow.

```yaml
kiosk:
  override_max_duration: 7200          # seconds; default and longest override (default 2 h)
  override_source_refresh_interval: 5  # minutes between reloading the image lists (default 5)
  override_sources:
    - key: "recipes"                   # used in MQTT topics and commands
      name: "Kitchen recipes"          # shown in HA as "Override: Kitchen recipes"
      album: "abc123-immich-album-id"  # Immich album ID (from the album URL)
```

Only albums in `override_sources` can be shown. The key `duration` is reserved.

**From the HA device page:** pick an image in **Override: Kitchen recipes**.
Pick `off` to stop. **Override: duration** sets how long the image stays.

**Image names:** each image is listed by its **description in Immich**
(the info panel → "Add a description"). Images without a description are listed
by filename. If two images have the same description, the second one is listed
by filename.

**New images:** the list is reloaded every `override_source_refresh_interval`
minutes. Send `refresh-sources` (below) to reload it at once, e.g. right after
adding a recipe.

**From automations and scripts**, publish to `immich-kiosk/override/command`
(all screens) or `immich-kiosk/<name>/override/command`:

| Payload | Effect |
|---|---|
| `start <key> <name>` | Show one image; `<name>` is the description or the filename |
| `start <key>` | Show the whole album as a slideshow |
| `stop` | End the override |
| `refresh-sources` | Reload all image lists |
| `refresh-sources <key>` | Reload one image list |

`start` uses **Override: duration** of the device it is sent to.

## Home Assistant examples

The entity IDs below are examples. Find yours on the device page or in
Developer tools → States.

Show a message on the kitchen screen:

```yaml
action: mqtt.publish
data:
  topic: immich-kiosk/kitchen/message/command
  payload: '{"text": "Dinner is ready!", "duration": 10, "size": "large"}'
```

Show a message on all screens using the HA entities:

```yaml
- action: text.set_value
  target:
    entity_id: text.immich_kiosk_all_message_content
  data:
    value: "Leaving in 5 minutes"
- action: button.press
  target:
    entity_id: button.immich_kiosk_all_message_send
```

Show a recipe in the kitchen for 90 minutes:

```yaml
- action: number.set_value
  target:
    entity_id: number.immich_kiosk_kitchen_override_duration
  data:
    value: 90
- action: select.select_option
  target:
    entity_id: select.immich_kiosk_kitchen_override_kitchen_recipes
  data:
    option: "Lasagne"
```

Next image on every screen:

```yaml
action: mqtt.publish
data:
  topic: immich-kiosk/command
  payload: next
```

For a fixed layout, put the entities in an **Entities** card on a dashboard in
the order you want; the device page always sorts by name.

## MQTT topic reference

`<prefix>` is `mqtt_topic_prefix` (default `immich-kiosk`). `[/<client>]` means
the topic exists both without a client name (all screens) and with one.

| Topic | Direction | Payload |
|---|---|---|
| `<prefix>/status` | Kiosk → | `online` / `offline` (retained) |
| `<prefix>[/<client>]/command` | → Kiosk | `next`, `previous` |
| `<prefix>[/<client>]/message/command` | → Kiosk | `show`, `clear`, text or JSON |
| `<prefix>[/<client>]/message/text/set` | → Kiosk | message text (from HA) |
| `<prefix>[/<client>]/message/text/state` | Kiosk → | current text (retained) |
| `<prefix>[/<client>]/message/duration/set` | → Kiosk | minutes (from HA) |
| `<prefix>[/<client>]/message/duration/state` | Kiosk → | current minutes (retained) |
| `<prefix>[/<client>]/override/command` | → Kiosk | `start …`, `stop`, `refresh-sources …` |
| `<prefix>[/<client>]/override/<key>/set` | → Kiosk | image name or `off` (from HA) |
| `<prefix>[/<client>]/override/<key>/state` | Kiosk → | active image name or `off` (retained) |
| `<prefix>[/<client>]/override/duration/set` | → Kiosk | minutes (from HA) |
| `<prefix>[/<client>]/override/duration/state` | Kiosk → | current minutes (retained) |

Discovery configs are published under `homeassistant/…`. Kiosk listens on
`homeassistant/status` and republishes its states when HA comes back online.

## Good to know

- **Kept in memory:** active messages and overrides end when Kiosk restarts. The
  content and duration fields are kept (retained in MQTT).
- **Entity IDs** are set by HA when an entity is first created. Renaming an
  entity later only changes its name. To get fresh IDs, delete the device in HA;
  Kiosk recreates it (the "all" device on restart, a client device when that
  screen reconnects).
- **A screen does not react:** check that its URL has the right `?client=`
  name, and that the Kiosk log shows `MQTT connected`.
- **Updating a Docker install:** pull the new image and recreate the container;
  `docker restart` keeps running the old image.
