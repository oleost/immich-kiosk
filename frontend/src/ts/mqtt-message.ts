/**
 * mqtt-message.ts
 * Shows text messages sent via MQTT on top of the slideshow.
 */

type MessagePayload = {
    text: string;
    position?: string;
    size?: string;
    remaining?: number; // seconds
};

const VISIBLE_CLASS = "kiosk-message--visible";

let messageEl: HTMLDivElement | null = null;
let hideTimer: number | undefined;

function getMessageElement(): HTMLDivElement {
    if (!messageEl) {
        messageEl = document.createElement("div");
        messageEl.id = "kiosk-message";
        messageEl.setAttribute("role", "status");
        messageEl.setAttribute("aria-live", "polite");
        document.body.appendChild(messageEl);
    }
    return messageEl;
}

function hideMessage(): void {
    window.clearTimeout(hideTimer);
    messageEl?.classList.remove(VISIBLE_CLASS);
}

/**
 * Handles the data of a kiosk-message SSE event. An empty text hides the
 * current message.
 */
export function handleMessageEvent(data: string): void {
    let payload: MessagePayload;
    try {
        payload = JSON.parse(data) as MessagePayload;
    } catch {
        console.warn("immich-kiosk: invalid message event:", data);
        return;
    }

    if (!payload.text) {
        hideMessage();
        return;
    }

    const el = getMessageElement();
    // textContent keeps any HTML in the message from being rendered.
    el.textContent = payload.text;
    el.className = [
        "kiosk-message",
        `kiosk-message--position-${payload.position ?? "bottom"}`,
        `kiosk-message--size-${payload.size ?? "medium"}`,
    ].join(" ");

    // Force a style flush so the fade-in runs when the element was just created.
    void el.offsetWidth;
    el.classList.add(VISIBLE_CLASS);

    window.clearTimeout(hideTimer);
    if (payload.remaining && payload.remaining > 0) {
        hideTimer = window.setTimeout(hideMessage, payload.remaining * 1000);
    }
}
