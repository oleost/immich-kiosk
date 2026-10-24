# Immich Kiosk – oleost fork

<div align="center">
  <img src="https://raw.githubusercontent.com/oleost/immich-kiosk/main/assets/logo.svg" width="200" height="auto" alt="Immich Kiosk windmill logo" />
</div>
<br />

> [!WARNING]
> **This is an unofficial personal fork of [Immich Kiosk](https://github.com/damongolding/immich-kiosk).**
> The fork-specific features below were written largely with AI assistance
> and are tested only on my own setup. They are not part of
> upstream Kiosk and have not been reviewed by its author. Use them at your own
> risk.
>
> **Please do not report problems with this fork to the upstream project.**
> Open an issue [in this repository](https://github.com/oleost/immich-kiosk/issues) instead.

## What this fork adds

The main additions are about controlling Kiosk from Home Assistant over MQTT:

- **Remote control** – next/previous image, for all screens or one named screen
  (`?client=kitchen`). Devices and entities appear in Home Assistant
  automatically.
- **Text messages** – show a banner such as "Dinner is ready!" on top of the
  slideshow for a set number of minutes.
- **Timed override** – show one image (or album) from a pre-approved list for a
  set time, e.g. tonight's recipe in the kitchen. Images are picked by their
  Immich description.
- **Album and person focus** – show only one of the albums or people from your
  config, on all screens or one screen, until you switch back to `Default`.
  Albums in `albums_on_demand` are only shown when picked.
- **Pause, sleep and reload** – freeze the image, put the screen to sleep or
  reload the page from Home Assistant.
- **Filters** – show only favorites, or only memories.
- **What is showing** – a sensor with the current image's description, date,
  place and people, and a small preview image.

**How to set it up and use it: [MQTT.md](https://github.com/oleost/immich-kiosk/blob/main/MQTT.md).**

Everything else works exactly like upstream Kiosk, so for installation and all
other settings use the [official documentation](https://docs.immichkiosk.app).

## Using the fork

Use this image instead of the upstream one:

```yaml
services:
  immich-kiosk:
    image: ghcr.io/oleost/immich-kiosk:latest
```

`:latest` is rebuilt on every push to `main` (linux/amd64 and linux/arm64).
There are no versioned releases. To update, pull the image and recreate the
container (`docker restart` keeps the old image).

The fork is regularly rebased onto upstream, so it follows upstream releases.
[FORK.md](https://github.com/oleost/immich-kiosk/blob/main/FORK.md) lists every
change compared to upstream.

## Credits

Immich Kiosk is made by [damongolding](https://github.com/damongolding); almost
all of the code here is their work.

Kiosk is not affiliated with [Immich](https://github.com/immich-app/immich).

## License

[AGPL-3.0](https://github.com/oleost/immich-kiosk/blob/main/LICENSE), the same
as upstream.
