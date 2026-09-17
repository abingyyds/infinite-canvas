import { nanoid } from "nanoid";

import i18n from "@/i18n";

const apiText = (key: string, options?: Record<string, unknown>) => i18n.t(`apiErrors.${key}`, options);

/** 网关统一任务读模型：/v1/tasks/{id} 对图片和视频返回同一种信封。 */
export type MediaTask = {
    id: string;
    status: string;
    result?: { images?: Array<{ index?: number; url?: string; b64_json?: string }> };
    error?: { message?: string; code?: string };
};

// 图片任务比视频短得多，但网关代持同步上游时也可能排队，留够余量。
const IMAGE_POLL_BUDGET_MS = 15 * 60 * 1000;

export function imagePollTimedOut(startedAt: number, now: number) {
    return now - startedAt >= IMAGE_POLL_BUDGET_MS;
}

/** 网关配了 ServerAddress 就返回绝对地址；没配时是相对路径，得用 baseUrl 的 origin 补全。 */
export function resolveTaskMediaUrl(url: string | undefined, baseUrl: string) {
    const value = (url || "").trim();
    if (!value) return "";
    if (/^(https?:|data:|blob:)/i.test(value)) return value;
    try {
        return new URL(value, new URL(baseUrl).origin).toString();
    } catch {
        return value;
    }
}

export function imagesFromTask(task: MediaTask, baseUrl: string) {
    const images = (task.result?.images || [])
        .map((item) => {
            const url = resolveTaskMediaUrl(item.url, baseUrl);
            if (url) return { id: nanoid(), dataUrl: url };
            const inline = (item.b64_json || "").trim();
            return inline ? { id: nanoid(), dataUrl: `data:image/png;base64,${inline}` } : null;
        })
        .filter((item): item is { id: string; dataUrl: string } => item !== null);
    if (!images.length) throw new Error(apiText("noImageReturned"));
    return images;
}

/** 终态失败返回原因，未结束返回空串。 */
export function asyncImageTaskFailure(task: MediaTask) {
    if (task.status !== "failed") return "";
    return (task.error?.message || "").trim() || apiText("requestFailed");
}

// 网关拒绝受理异步任务的两种情形，都不是请求本身有问题
const UNSUPPORTED_FACADE_HINTS = ["async facade does not support", "require media storage"];

/**
 * 这个网关/渠道是否根本没有异步门面，需要退回同步路径。只认两种情形：路由不存在（404），
 * 以及网关明说渠道类型不支持。别的 4xx 是真实请求错误，退回同步只会再花一次钱。
 */
export function isAsyncFacadeUnsupported(error: unknown) {
    const response = (error as { response?: { status?: number; data?: unknown } })?.response;
    if (!response) return false;
    if (response.status === 404) return true;
    if (response.status !== 400) return false;
    const message = ((response.data as { error?: { message?: string } })?.error?.message || "").toLowerCase();
    return UNSUPPORTED_FACADE_HINTS.some((hint) => message.includes(hint));
}

/** ponytail: 和 video.ts 里那份一样；为它建个公共模块要顺带改动视频那条路，等第三处再提。 */
export function delay(ms: number, signal?: AbortSignal) {
    return new Promise<void>((resolve, reject) => {
        if (signal?.aborted) {
            reject(new DOMException("Aborted", "AbortError"));
            return;
        }
        const timer = setTimeout(resolve, ms);
        signal?.addEventListener(
            "abort",
            () => {
                clearTimeout(timer);
                reject(new DOMException("Aborted", "AbortError"));
            },
            { once: true },
        );
    });
}
