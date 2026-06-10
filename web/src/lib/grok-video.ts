export const GROK_PREVIEW_VIDEO_MODEL = "grok-imagine-video-1.5-preview";

export const grokPreviewDurationOptions = [6, 10, 15] as const;

const grokRatioSizes = {
    "16:9": "1280x720",
    "9:16": "720x1280",
    "1:1": "1024x1024",
    "4:3": "960x720",
    "3:4": "720x960",
    "21:9": "1280x544",
} as const;

export function isGrokImagineVideoModel(model: string) {
    return model.toLowerCase().includes("grok-imagine-video");
}

export function isGrokPreviewVideoModel(model: string) {
    return model.toLowerCase().includes(GROK_PREVIEW_VIDEO_MODEL);
}

export function normalizeGrokPreviewModel(model: string) {
    return isGrokPreviewVideoModel(model) ? GROK_PREVIEW_VIDEO_MODEL : model.trim();
}

export function normalizeGrokPreviewSeconds(value: string, model = "") {
    const modelSeconds = model.toLowerCase().match(/grok-imagine-video-1\.5-preview-(6|10|15)s?$/)?.[1];
    if (modelSeconds) return modelSeconds;
    const seconds = Math.floor(Number(value) || 6);
    if (grokPreviewDurationOptions.includes(seconds as (typeof grokPreviewDurationOptions)[number])) return String(seconds);
    const nearest = grokPreviewDurationOptions.reduce((best, item) => (Math.abs(item - seconds) < Math.abs(best - seconds) ? item : best), 6);
    return String(nearest);
}

export function normalizeGrokVideoSize(value: string) {
    if (/^\d+x\d+$/.test(value || "")) return value;
    const ratio = normalizeGrokVideoAspectRatio(value);
    return grokRatioSizes[ratio] || grokRatioSizes["16:9"];
}

export function normalizeGrokPreviewVideoSize(value: string) {
    return normalizeGrokVideoSize(value || "16:9");
}

export function normalizeGrokVideoAspectRatio(value: string) {
    if (value in grokRatioSizes) return value as keyof typeof grokRatioSizes;
    const match = String(value || "").match(/^(\d+)x(\d+)$/);
    if (!match) return "16:9";
    const width = Number(match[1]);
    const height = Number(match[2]);
    if (!width || !height) return "16:9";
    const ratio = width / height;
    const options = [
        ["16:9", 16 / 9],
        ["9:16", 9 / 16],
        ["1:1", 1],
        ["4:3", 4 / 3],
        ["3:4", 3 / 4],
        ["21:9", 21 / 9],
    ] as const;
    return options.reduce((best, item) => (Math.abs(item[1] - ratio) < Math.abs(best[1] - ratio) ? item : best), options[0])[0];
}
