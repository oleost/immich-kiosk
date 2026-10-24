# MQTT and Home Assistant

This fork can be controlled over MQTT, with entities created automatically in
Home Assistant (HA). You can:

- **Navigate** – go to the next or previous image, or reload the page.
- **Pause and sleep** – freeze the current image, or put the screen to sleep
  ("nobody home", "TV is on").
- **Show a text message** – a banner on top of the slideshow for a set time
  ("Dinner is ready!").
- **Override the slideshow** – show one image or album from a pre-approved list
  for a set time (tonight's recipe in the kitchen).
- **Focus on one album or person** – show only one of the albums or people from
  your config until you switch back.
- **Filter** – show only favorites, or only memories ("on this day").
- **See what is showing** – the current image's description, date, place and
  people, with a small preview, on your dashboard.

Everything works for all screens at once, or for a single named screen.

- [Setup](#setup)
- [Naming screens](#naming-screens)
- [What appears in Home Assistant](#what-appears-in-home-assistant)
- [Navigation](#navigation)
- [Pause and sleep](#pause-and-sleep)
- [Text messages](#text-messages)
- [Override](#override)
- [Album and person focus](#album-and-person-focus)
- [Filters](#filters)
- [What is showing](#what-is-showing)
- [How the controls combine](#how-the-controls-combine)
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

Everything except override is available as soon as MQTT is enabled. Album and
person focus need at least two albums or people in the config, see
[Album and person focus](#album-and-person-focus). Override needs extra
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
| Album: show | select | Controls | Show only one album, or `Default` |
| Filter: favorites | switch | Controls | Show only favorites |
| Filter: memories | switch | Controls | Show only memories ("on this day") |
| Image: next | button | Controls | Next image |
| Image: previous | button | Controls | Previous image |
| Message: send | button | Controls | Shows *Message: content* for *Message: duration* |
| Message: stop | button | Controls | Removes the message |
| Override: &lt;source name&gt; | select | Controls | One per override source: pick an image, or `off` |
| Page: reload | button | Controls | Reloads the page in the browser |
| Person: show | select | Controls | Show only one person, or `Default` |
| Slideshow: pause | switch | Controls | Freezes the current image |
| Slideshow: sleep | switch | Controls | Puts the screen to sleep |
| Image: current | sensor | Sensors | Description (or filename) of the image shown, details as attributes |
| Image: preview | image | Sensors | A small copy of the image shown |
| Message: content | text | Configuration | The message text (max 255 characters) |
| Message: duration | number | Configuration | Minutes the message stays (1–1440, default 5) |
| Override: duration | number | Configuration | Minutes an override stays (1 to `override_max_duration`) |

The override entities only exist when `override_sources` is configured.
*Album: show* and *Person: show* only exist on a device with at least two
albums or people, see [Album and person focus](#album-and-person-focus).

HA sorts entities alphabetically by name within each section, which is why the
names have prefixes. Text, numbers, switches and selects are stored as retained
MQTT messages, so they survive a Kiosk restart.

## Navigation

Press **Image: next** / **Image: previous** / **Page: reload**, or publish
`next`, `previous` or `reload` to:

```
immich-kiosk/command            # all screens
immich-kiosk/kitchen/command    # only "kitchen"
```

`reload` reloads the page, e.g. on a screen that hangs or after changing the
config. Unlike `next`, it only reaches screens that are connected right now; it
is not kept for a screen that connects a few seconds later.

## Pause and sleep

**Slideshow: pause** freezes the current image. It stays paused when you press
*Image: next* or *Image: previous* (the new image is shown and stays), and after
a page reload. Switch it off to continue. A video is paused too. The pause from
HA does not show the on-screen menu; tapping the screen still pauses and
resumes as usual.

**Slideshow: sleep** puts the screen in Kiosk's normal sleep mode: a black
screen with the dimmed clock and the sleep icon, and the screen dimmed when
`sleep_dim_screen` is set (Fully Kiosk, Immich Frame). No new images are loaded
while it is on. Switch it off to wake up. If your `sleep_start` / `sleep_end`
schedule says it is night, the screen goes back to sleep at its next check
(within 15 seconds). The switch works with or without a schedule, and also
when `disable_sleep` is set.

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

## Album and person focus

With several albums in `albums:`, Kiosk normally picks images from all of them
(see the upstream docs for weighting). **Album: show** lets you narrow the
slideshow down to one of them until you switch back. **Person: show** does the
same for the people in `people:`. Nothing new is needed in `config.yaml`.

```yaml
albums:
  - "id-of-summer-2025"
  - "id-of-cabin"
  - "id-of-birthdays"
```

gives the options `Default`, `Birthdays`, `Cabin` and `Summer 2025`.

**Albums only shown on demand:** albums in `albums_on_demand:` are never part of
`Default`. They only appear when you pick them in **Album: show**, or when a
screen opens them with `?album=<id>` in its URL (which works for any album).

```yaml
albums:
  - "id-of-summer-2025"
  - "id-of-cabin"
albums_on_demand:
  - "id-of-christmas"    # only when picked
```

`Default` shows Summer 2025 and Cabin; `Christmas` is an extra option on every
device. One album in `albums:` plus one in `albums_on_demand:` is enough to get
the select.

- **`Default`** is the normal slideshow: the albums from `config.yaml`, or the
  albums in the screen's URL (`?album=…`), mixed with any people, tags, dates
  or memories you have configured. Nothing changes until you pick something
  else.
- **An album** shows only that album. People, tags, dates and memories are left
  out; filters such as `filter_date` still apply.
- **A person** shows only that person, in the same way.
- On one device you can focus on an album **or** a person: picking one sets the
  other back to `Default`.

**All screens or one screen:**

- On **Immich Kiosk (all)** the options are the albums from `config.yaml`
  (`albums:` and `albums_on_demand:`). The
  choice applies to every screen, also screens with their own albums in the
  URL. Back on `Default`, every screen shows its own albums again.
- On a screen's device the options are the albums that screen uses (from its
  URL, or `config.yaml` when the URL has none), plus `albums_on_demand:`. The choice applies only to that
  screen and wins over the "all" device. `Default` here means "follow the all
  device".
- An [override](#override) wins while it lasts. Afterwards the screen goes back
  to the focused album.

**Names:** albums and people are listed by their name in Immich. The keywords
`all`, `owned`, `shared` and `favorites` are listed as `All albums`,
`Owned albums`, `Shared albums` and `Favorites`, and `all` in `people:` as
`All people`. An album or person of another user (`id@user`) gets the user name
added. A person without a name in Immich, or an album that cannot be found, is
listed as `Unnamed (…)` with the first 8 characters of its ID. Two items with
the same name get `(2)`, `(3)` … added. The names are read when Kiosk starts
and when the list changes; restart Kiosk after renaming an album or person in
Immich.

The people options follow the same rules as the albums: the "all" device offers
`people:` from `config.yaml`, a screen's device the people that screen uses
(`?person=…` in its URL, or `config.yaml`). There is no `people_on_demand`.

**When the select appears:** on the "all" device when Kiosk starts. On a
screen's device once that screen has loaded its first image, since only then
does Kiosk know which albums and people the screen uses. If the screen uses
fewer than two it gets no select, but still follows the "all" device.

**Kept across restarts:** the choice is stored as a retained MQTT state. After a
restart a screen's own choice takes effect when the screen loads its first
image. If the chosen album is no longer in the list, the select shows `Default`.

**From automations and scripts**, publish the name (or `Default`) to
`immich-kiosk[/<name>]/album/set` or `immich-kiosk[/<name>]/person/set`.

## Filters

**Filter: favorites** shows only images marked as favorite in Immich. It works
on top of the normal slideshow and on top of an album or person focus
("favorites from the cabin album"). It is the same as `filter_favorites`.

**Filter: memories** shows only Immich memories ("on this day" in earlier
years), instead of the normal slideshow or a focus. When Immich has no memories
for today, the screen shows its normal slideshow.

If a filter or focus leaves nothing to show (e.g. a person without favorites),
the screen shows its normal slideshow, and the Kiosk log says
`nothing to show with the Home Assistant controls`.

**From automations and scripts**, publish `ON` or `OFF` to
`immich-kiosk[/<name>]/favorites/set` or `immich-kiosk[/<name>]/memories/set`.

## What is showing

**Image: current** shows the description of the image on screen, or its
filename when it has no description. Its attributes hold the details, for
cards, templates and automations:

| Attribute | Example |
|---|---|
| `description` | `Grandma's 80th` |
| `date` | `2024-07-14 18:32` (local time of the photo) |
| `city`, `state`, `country` | `Bergen`, `Vestland`, `Norway` |
| `people` | `["Anna", "Ola"]` (named people only) |
| `album` | `Cabin` (when the image was picked from an album) |
| `memory` | `3 years ago` (when it is a memory) |
| `source` | `album`, `person`, `memories`, `date`, `tag`, `random` … |
| `favorite` | `true` / `false` |
| `type` | `image` or `video` |
| `filename`, `asset_id` | the Immich filename and ID |
| `immich_url` | link to the image in Immich (`immich_external_url` when set) |
| `client` | the screen that showed it |

Attributes without a value are left out.

**Image: preview** is a copy of the image, at most 640 pixels wide or high. Put
it in a **Picture entity** or **Image** card.

A screen's device shows that screen's image. The "all" device shows the last
image shown on any screen, which is handy with a single screen or for screens
without a `?client=` name. In a split view only the first image is reported.

The sensor changes with every image, so it fills the activity log and the
history database. To keep it out, add this to HA's `configuration.yaml`:

```yaml
recorder:
  exclude:
    entity_globs:
      - sensor.immich_kiosk_*_image_current
      - image.immich_kiosk_*_image_preview
logbook:
  exclude:
    entity_globs:
      - sensor.immich_kiosk_*_image_current
      - image.immich_kiosk_*_image_preview
```

The entities still show the current image; only their history is not stored.

## How the controls combine

- A setting on a screen's device wins over the same setting on the "all"
  device. For the selects, `Default` on a screen means "follow the all
  device".
- A switch that is on for the "all" device applies to every screen; a screen's
  own switch can turn it on for that screen, but not off.
- An [override](#override) wins over focus and filters while it lasts.
- **Filter: memories** wins over a focus; **Filter: favorites** combines with
  it.
- Changes take effect at once: the screen moves on to a new image.

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

Show only the cabin album on every screen, and back to normal:

```yaml
- action: select.select_option
  target:
    entity_id: select.immich_kiosk_all_album_show
  data:
    option: "Cabin"
- action: select.select_option
  target:
    entity_id: select.immich_kiosk_all_album_show
  data:
    option: "Default"
```

Pause the kitchen screen while someone looks at a photo, and continue:

```yaml
- action: switch.turn_on
  target:
    entity_id: switch.immich_kiosk_kitchen_slideshow_pause
- action: switch.turn_off
  target:
    entity_id: switch.immich_kiosk_kitchen_slideshow_pause
```

Put every screen to sleep when nobody is home:

```yaml
triggers:
  - trigger: numeric_state
    entity_id: zone.home
    below: 1
actions:
  - action: switch.turn_on
    target:
      entity_id: switch.immich_kiosk_all_slideshow_sleep
```

Show when and where the current photo was taken, in a Markdown card:

```yaml
type: markdown
content: >
  {{ state_attr('sensor.immich_kiosk_all_image_current', 'date') }} –
  {{ state_attr('sensor.immich_kiosk_all_image_current', 'city') }}
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
| `<prefix>[/<client>]/command` | → Kiosk | `next`, `previous`, `reload` |
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
| `<prefix>[/<client>]/album/set` | → Kiosk | album name or `Default` (from HA) |
| `<prefix>[/<client>]/album/state` | Kiosk → | focused album or `Default` (retained) |
| `<prefix>[/<client>]/person/set` | → Kiosk | person name or `Default` (from HA) |
| `<prefix>[/<client>]/person/state` | Kiosk → | focused person or `Default` (retained) |
| `<prefix>[/<client>]/<switch>/set` | → Kiosk | `ON` / `OFF`; `<switch>` is `pause`, `sleep`, `favorites` or `memories` |
| `<prefix>[/<client>]/<switch>/state` | Kiosk → | `ON` / `OFF` (retained) |
| `<prefix>[/<client>]/image/state` | Kiosk → | description or filename of the image shown (retained) |
| `<prefix>[/<client>]/image/attributes` | Kiosk → | JSON with the details (retained) |
| `<prefix>[/<client>]/image/preview` | Kiosk → | JPEG, at most 640 px (retained) |

Discovery configs are published under `homeassistant/…`. Kiosk listens on
`homeassistant/status` and republishes its states when HA comes back online.

## Good to know

- **Kept in memory:** active messages and overrides end when Kiosk restarts. The
  content and duration fields, the switches and the album and person focus are
  kept (retained in MQTT).
- **Offline mode:** with `use_offline_mode`, images come from files Kiosk saved
  earlier, so album and person focus and the filters have no effect. Pause,
  sleep, reload, messages and navigation still work.
- **Entity IDs** are set by HA when an entity is first created. Renaming an
  entity later only changes its name. To get fresh IDs, delete the device in HA;
  Kiosk recreates it (the "all" device on restart, a client device when that
  screen reconnects).
- **A screen does not react:** check that its URL has the right `?client=`
  name, and that the Kiosk log shows `MQTT connected`.
- **Updating a Docker install:** pull the new image and recreate the container;
  `docker restart` keeps running the old image.
