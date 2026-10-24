/**
 * mqtt-control.ts
 * Applies the pause and sleep switches set in Home Assistant, sent as
 * kiosk-control events on /events.
 */

import htmx from "htmx.org";
import { pausePolling, resumePolling } from "./polling";

type ControlState = {
    pause: boolean;
    sleep: boolean;
};

/**
 * Element that receives the sleep state. The page only has it while a sleep
 * schedule is set; otherwise it is created, so the sleep styles apply.
 */
const SLEEP_CONTROLLER_ID = "sleep-controller";

const ASSET_PATH = /^\/asset\/(new|offline|previous)$/;

let state: ControlState = { pause: false, sleep: false };

/**
 * Keeps a Home Assistant pause in place when a new asset is shown, e.g. after
 * "next". Must be called after kiosk.ts registers its own listeners, which
 * restart polling on every asset.
 */
export function initMqttControl(): void {
    htmx.on("htmx:afterRequest", (event: Event) => {
        const path = (event as CustomEvent).detail?.pathInfo?.requestPath || "";
        if (state.pause && ASSET_PATH.test(path)) {
            pausePolling(false);
        }
    });

    // A video starts playing when it is swapped in, after the request ended.
    htmx.on("htmx:afterSettle", () => {
        if (state.pause) {
            pausePolling(false);
        }
    });
}

/**
 * Handles a kiosk-control event: {"pause": bool, "sleep": bool}.
 */
export function handleControlEvent(data: string): void {
    let next: ControlState;
    try {
        next = { ...state, ...(JSON.parse(data) as Partial<ControlState>) };
    } catch {
        console.warn("immich-kiosk: invalid control event:", data);
        return;
    }

    const previous = state;
    state = next;

    if (next.pause !== previous.pause) {
        if (next.pause) {
            pausePolling(false);
        } else {
            resumePolling();
        }
    }

    if (next.sleep !== previous.sleep) {
        refreshSleep();
    }
}

/**
 * Asks the server for the sleep state, so the sleep schedule, the sleep icon
 * and screen dimming (Fully Kiosk, Immich Frame) work as usual.
 */
function refreshSleep(): void {
    let target = document.getElementById(SLEEP_CONTROLLER_ID);

    if (!target) {
        target = document.createElement("div");
        target.id = SLEEP_CONTROLLER_ID;
        document.body.appendChild(target);
    }

    htmx.ajax("get", `/sleep${window.location.search}`, {
        target,
        swap: "innerHTML",
    });
}
