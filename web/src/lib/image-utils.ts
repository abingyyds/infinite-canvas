import type { ReferenceImage } from "@/types/image";

const DEFAULT_UPLOAD_MAX_EDGE = 2048;
const DEFAULT_UPLOAD_MAX_BYTES = 15 * 1024 * 1024;
const MIN_UPLOAD_EDGE = 512;

export function formatBytes(bytes: number) {
    if (!Number.isFinite(bytes) || bytes <= 0) {
        return "";
    }
    const units = ["B", "KB", "MB", "GB"];
    let value = bytes;
    let unitIndex = 0;
    while (value >= 1024 && unitIndex < units.length - 1) {
        value /= 1024;
        unitIndex += 1;
    }
    return `${value >= 10 || unitIndex === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[unitIndex]}`;
}

export function formatDuration(ms: number) {
    const value = Math.max(0, Math.floor(ms / 1000));
    const minutes = Math.floor(value / 60);
    const seconds = value % 60;
    return minutes ? `${minutes}分${String(seconds).padStart(2, "0")}秒` : `${seconds}秒`;
}

export function getDataUrlByteSize(dataUrl: string) {
    const base64 = dataUrl.split(",", 2)[1];
    if (!base64) {
        return 0;
    }
    const padding = base64.endsWith("==") ? 2 : base64.endsWith("=") ? 1 : 0;
    return Math.max(0, Math.floor((base64.length * 3) / 4) - padding);
}

export function readFileAsDataUrl(file: File) {
    return new Promise<string>((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(String(reader.result || ""));
        reader.onerror = () => reject(new Error("读取图片失败"));
        reader.readAsDataURL(file);
    });
}

export function readImageMeta(dataUrl: string) {
    return new Promise<{ width: number; height: number; mimeType: string }>((resolve) => {
        const image = new Image();
        const done = () => resolve({ width: image.naturalWidth || 1024, height: image.naturalHeight || 1024, mimeType: dataUrl.match(/^data:([^;]+)/)?.[1] || "image/png" });
        image.onload = done;
        image.onerror = done;
        setTimeout(done, 3000);
        image.src = dataUrl;
    });
}

export async function normalizeImageDataUrlForUpload(dataUrl: string, options: { maxEdge?: number; maxBytes?: number; targetWidth?: number; targetHeight?: number } = {}) {
    const image = await loadImage(dataUrl);
    const sourceWidth = image.naturalWidth || image.width || 1024;
    const sourceHeight = image.naturalHeight || image.height || 1024;
    const maxEdge = options.maxEdge || DEFAULT_UPLOAD_MAX_EDGE;
    const maxBytes = options.maxBytes || DEFAULT_UPLOAD_MAX_BYTES;
    let width = options.targetWidth || sourceWidth;
    let height = options.targetHeight || sourceHeight;

    if (!options.targetWidth || !options.targetHeight) {
        const scale = Math.min(1, maxEdge / Math.max(sourceWidth, sourceHeight));
        width = Math.max(1, Math.round(sourceWidth * scale));
        height = Math.max(1, Math.round(sourceHeight * scale));
    }

    for (let attempt = 0; attempt < 5; attempt += 1) {
        const normalized = drawImageToPngDataUrl(image, width, height);
        const bytes = getDataUrlByteSize(normalized);
        if (bytes <= maxBytes || options.targetWidth || Math.max(width, height) <= MIN_UPLOAD_EDGE) {
            return { dataUrl: normalized, width, height, bytes, mimeType: "image/png" };
        }
        const scale = Math.max(0.5, Math.min(0.9, Math.sqrt(maxBytes / bytes) * 0.95));
        width = Math.max(MIN_UPLOAD_EDGE, Math.round(width * scale));
        height = Math.max(MIN_UPLOAD_EDGE, Math.round(height * scale));
    }

    const normalized = drawImageToPngDataUrl(image, width, height);
    return { dataUrl: normalized, width, height, bytes: getDataUrlByteSize(normalized), mimeType: "image/png" };
}

export function dataUrlToFile(image: ReferenceImage) {
    const [header, content] = image.dataUrl.split(",", 2);
    const mimeType = header.match(/data:(.*?);base64/)?.[1] || image.type || "image/png";
    const binary = atob(content || "");
    const bytes = new Uint8Array(binary.length);
    for (let index = 0; index < binary.length; index += 1) {
        bytes[index] = binary.charCodeAt(index);
    }
    return new File([bytes], image.name || "reference.png", { type: mimeType });
}

function loadImage(dataUrl: string) {
    return new Promise<HTMLImageElement>((resolve, reject) => {
        const image = new Image();
        image.onload = () => resolve(image);
        image.onerror = () => reject(new Error("参考图读取失败，请重新上传图片"));
        image.src = dataUrl;
    });
}

function drawImageToPngDataUrl(image: HTMLImageElement, width: number, height: number) {
    const canvas = document.createElement("canvas");
    canvas.width = width;
    canvas.height = height;
    const context = canvas.getContext("2d");
    if (!context) throw new Error("参考图处理失败，请重新上传图片");
    context.drawImage(image, 0, 0, width, height);
    return canvas.toDataURL("image/png");
}
