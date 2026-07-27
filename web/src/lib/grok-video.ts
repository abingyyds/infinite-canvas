export const GROK_IMAGE_VIDEO_MODEL = "grok-imagine-video-1.5";
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
    return model.toLowerCase().includes("grok-imagine-video") || isGrokPreviewVideoModel(model);
}

export function isGrokPreviewVideoModel(model: string) {
    const name = model.toLowerCase();
    // "grok-video-1.5" is the SubRouter-style name for the same first-frame video model.
    return name.includes("grok-imagine-video-1.5") || name.includes("grok-video-1.5");
}

export function normalizeGrokPreviewModel(model: string) {
    return isGrokPreviewVideoModel(model) ? GROK_IMAGE_VIDEO_MODEL : model.trim();
}

export function normalizeGrokPreviewSeconds(value: string, model = "") {
    const modelSeconds = model.toLowerCase().match(/grok-imagine-video-1\.5(?:-preview)?-(1[0-5]|[1-9])s?$/)?.[1];
    if (modelSeconds) return modelSeconds;
    const seconds = Math.floor(Number(value) || 6);
    return String(Math.min(15, Math.max(1, seconds)));
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
