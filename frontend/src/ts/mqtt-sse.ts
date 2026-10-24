/**
 * mqtt-sse.ts
 * Listens to the /events SSE endpoint and translates incoming MQTT navigation
 * commands into the HTMX events that the kiosk already understands, shows
 * MQTT text messages and applies the pause and sleep switches.
 */

import htmx from "htmx.org";
import { handleControlEvent, initMqttControl } from "./mqtt-control";
import { handleMessageEvent } from "./mqtt-message";

type KioskCommand = "next" | "previous" | "reload";

/**
 * Initialises the SSE connection to /events.
 * Must be called after the kiosk element is available in the DOM.
 */
export function initMqttSSE(): void {
    initMqttControl();

    const params = new URLSearchParams(window.location.search);
    const url = params.toString() ? `/events?${params.toString()}` : "/events";
    const evtSource = new EventSource(url);

    evtSource.addEventListener("kiosk-command", (e: MessageEvent) => {
        const cmd = (e.data as string).trim() as KioskCommand;

        switch (cmd) {
            case "next": {
                const kioskEl = document.getElementById("kiosk");
                if (kioskEl) {
                    htmx.trigger(kioskEl, "kiosk-new-asset");
                }
                break;
            }
            case "previous": {
                const prevEl = document.getElementById(
                    "navigation-interaction-area--previous-asset",
                );
                if (prevEl) {
                    htmx.trigger(prevEl, "kiosk-prev-asset");
                }
                break;
            }
            case "reload":
                window.location.reload();
                break;
            default:
                console.warn("immich-kiosk: unknown MQTT command:", cmd);
        }
    });

    evtSource.addEventListener("kiosk-message", (e: MessageEvent) => {
        handleMessageEvent(e.data as string);
    });

    evtSource.addEventListener("kiosk-control", (e: MessageEvent) => {
        handleControlEvent(e.data as string);
    });

    evtSource.onerror = () => {
        // Browser will automatically reconnect for EventSource; nothing extra needed.
        console.debug("immich-kiosk: SSE connection error, will retry automatically");
    };
}
