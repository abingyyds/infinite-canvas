import axios from "axios";

import i18n from "@/i18n";
import { blobToDataUrl } from "@/services/image-storage";
import { useUserStore } from "@/stores/use-user-store";

export type ImageDownloadSource = { url: string; viaProxy?: boolean };

/** 只获取已经生成的图片，不调用模型；站内 JWT 不会转发给图片地址。 */
export async function downloadGeneratedImage(source: ImageDownloadSource, options?: { signal?: AbortSignal }) {
    if (!source.viaProxy) return source.url;
    const token = useUserStore.getState().token;
    try {
        const response = await axios.post<Blob>("/api/media-content", { url: source.url }, {
            responseType: "blob",
            headers: token ? { Authorization: `Bearer ${token}` } : undefined,
            signal: options?.signal,
        });
        return await blobToDataUrl(response.data);
    } catch (error) {
        if (axios.isCancel(error) || options?.signal?.aborted) throw error;
        const status = axios.isAxiosError(error) ? error.response?.status : undefined;
        const key = status === 401 ? "loginRequired" : status === 403 || status === 404 || status === 410 ? "linkUnavailable" : "failed";
        throw new Error(i18n.t(`imageDownload.${key}`));
    }
}
