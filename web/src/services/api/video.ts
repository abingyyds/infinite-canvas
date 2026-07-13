import axios from "axios";

import { GROK_IMAGE_VIDEO_MODEL, isGrokImagineVideoModel, isGrokPreviewVideoModel, normalizeGrokPreviewModel, normalizeGrokPreviewSeconds, normalizeGrokPreviewVideoSize, normalizeGrokVideoAspectRatio, normalizeGrokVideoSize } from "@/lib/grok-video";
import { dataUrlToFile } from "@/lib/image-utils";
import { getMediaBlob, uploadMediaFile, type UploadedFile } from "@/services/file-storage";
import { imageToDataUrl } from "@/services/image-storage";
import { boolConfig, buildSeedancePromptText, isArkSeedanceVideoConfig, isSeedanceVideoModel, normalizeSeedanceDuration, normalizeSeedanceRatio, normalizeSeedanceResolution, seedancePixelLabel, seedanceVideoReferenceError, SEEDANCE_REFERENCE_LIMITS } from "@/lib/seedance-video";
import { readVideoErrorMessage, readVideoStatus, readVideoTaskId, readVideoUrl, type VideoResponse } from "@/services/api/video-response";
import { buildApiUrl, type AiConfig } from "@/stores/use-config-store";
import { useUserStore } from "@/stores/use-user-store";
import type { ReferenceImage } from "@/types/image";
import type { ReferenceAudio, ReferenceVideo } from "@/types/media";
type ApiVideoResponse = VideoResponse | { code?: number; data?: VideoResponse | null; msg?: string };
type SeedanceTask = {
    id: string;
    status?: "queued" | "running" | "succeeded" | "failed" | "cancelled" | "expired";
    error?: { code?: string; message?: string } | null;
    content?: { video_url?: string; last_frame_url?: string } | null;
};
type ApiEnvelope<T> = T | { code?: number; data?: T | null; msg?: string };
type ReferenceMediaUploadResponse = { id: string; url: string; mimeType: string; bytes: number };
const VIDEO_RATIO_SIZES: Record<string, string> = {
    "16:9": "1280x720",
    "9:16": "720x1280",
    "1:1": "1024x1024",
    "4:3": "960x720",
    "3:4": "720x960",
    "2:3": "720x1080",
    "3:2": "1080x720",
    "21:9": "1280x544",
};

export type VideoGenerationResult = { blob?: Blob; url?: string; mimeType?: string };
export type VideoGenerationTask = { id: string; provider: "openai" | "seedance"; model: string; path?: string };
export type VideoGenerationTaskState = { status: "pending" } | { status: "completed"; result: VideoGenerationResult } | { status: "failed"; error: string };
export const VIDEO_TASK_TIMEOUT_MS = 10 * 60 * 1000;

function aiApiUrl(config: AiConfig, path: string) {
    return config.channelMode === "remote" ? `/api/v1${path}` : buildApiUrl(config.baseUrl, path);
}

function videoCreatePath(config: AiConfig, model: string) {
    return isGrokImagineVideoModel(model) && (config.channelMode === "remote" || isXAIBaseUrl(config.baseUrl)) ? "/videos/generations" : "/videos";
}

function aiHeaders(config: AiConfig, contentType?: string) {
    const token = useUserStore.getState().token;
    return config.channelMode === "remote"
        ? {
              ...(token ? { Authorization: `Bearer ${token}` } : {}),
              ...(contentType ? { "Content-Type": contentType } : {}),
          }
        : {
              Authorization: `Bearer ${config.apiKey}`,
              ...(contentType ? { "Content-Type": contentType } : {}),
          };
}

function refreshRemoteUser(config: AiConfig) {
    if (config.channelMode === "remote") void useUserStore.getState().hydrateUser();
}

export async function requestVideoGeneration(config: AiConfig, prompt: string, references: ReferenceImage[] = [], videoReferences: ReferenceVideo[] = [], audioReferences: ReferenceAudio[] = []): Promise<VideoGenerationResult> {
    const task = await createVideoGenerationTask(config, prompt, references, videoReferences, audioReferences);
    const delayMs = task.provider === "seedance" ? 5000 : 2500;
    const deadline = Date.now() + VIDEO_TASK_TIMEOUT_MS;
    for (;;) {
        const state = await pollVideoGenerationTask(config, task);
        if (state.status === "completed") return state.result;
        if (state.status === "failed") throw new Error(state.error);
        if (Date.now() >= deadline) throw new Error(`${task.provider === "seedance" ? "Seedance " : ""}视频生成超时，请稍后重试`);
        await delay(delayMs);
    }
}

export async function createVideoGenerationTask(config: AiConfig, prompt: string, references: ReferenceImage[] = [], videoReferences: ReferenceVideo[] = [], audioReferences: ReferenceAudio[] = []): Promise<VideoGenerationTask> {
    const model = (config.model || config.videoModel).trim();
    const isGrokVideo = isGrokImagineVideoModel(model);
    const isSeedanceVideo = isSeedanceVideoModel(model);
    const isVideoToVideo = isVideoToVideoModel(model);
    assertVideoConfig(config, model);
    if (isArkSeedanceVideoConfig({ ...config, model })) {
        return createSeedanceTask(config, model, prompt, references, videoReferences, audioReferences);
    }
    if ((videoReferences.length && !isGrokVideo && !isSeedanceVideo && !isVideoToVideo) || (audioReferences.length && !isGrokVideo && !isSeedanceVideo)) {
        throw new Error("当前视频接口只支持参考图；参考视频或参考音频请切换到 Grok Imagine Video、Seedance 2.0 / 火山 Agent Plan 模型，或移除参考素材");
    }
    if (isGrokPreviewVideoModel(model)) return createGrokPreviewVideoTask(config, model, prompt, references, videoReferences, audioReferences);
    if (isGrokVideo) return createGrokVideoTask(config, model, prompt, references, videoReferences, audioReferences);
    return createOpenAIVideoTask(config, model, prompt, references, isSeedanceVideo || isVideoToVideo ? videoReferences : [], isSeedanceVideo ? audioReferences : []);
}

export async function pollVideoGenerationTask(config: AiConfig, task: VideoGenerationTask): Promise<VideoGenerationTaskState> {
    assertVideoConfig(config, task.model);
    return task.provider === "seedance" ? pollSeedanceTask(config, task) : pollOpenAIVideoTask(config, task);
}

export async function storeGeneratedVideo(result: VideoGenerationResult): Promise<UploadedFile> {
    if (result.blob) return uploadMediaFile(result.blob, "video");
    if (result.url) return { url: result.url, storageKey: "", bytes: 0, mimeType: result.mimeType || "video/mp4" };
    throw new Error("视频接口没有返回可播放的视频");
}

async function createGrokPreviewVideoTask(config: AiConfig, model: string, prompt: string, references: ReferenceImage[], videoReferences: ReferenceVideo[], audioReferences: ReferenceAudio[]): Promise<VideoGenerationTask> {
    assertGrokPreviewReferences(references, videoReferences, audioReferences);
    const path = videoCreatePath(config, model);
    const official = path === "/videos/generations";
    const requestModel = official && config.channelMode !== "remote" ? normalizeGrokPreviewModel(model) : model;
    const firstFrame = official ? await resolveGrokImageUrl(config, references[0]) : await imageToDataUrl(references[0]);
    if (!firstFrame || (!firstFrame.startsWith("data:image/") && !isPublicMediaUrl(firstFrame))) throw new Error("首帧图片读取失败，请重新上传图片");
    const duration = Number(normalizeGrokPreviewSeconds(config.videoSeconds, model));
    const aspectRatio = normalizeGrokVideoAspectRatio(config.size || normalizeGrokPreviewVideoSize(config.size));
    const payload = official
        ? {
              model: requestModel,
              image: { url: firstFrame },
              duration,
              aspect_ratio: aspectRatio,
          }
        : {
              model,
              prompt: prompt.trim() || "animate",
              image: firstFrame,
              duration,
              seconds: String(duration),
              aspect_ratio: aspectRatio,
          };

    try {
        const created = unwrapVideoResponse((await axios.post<ApiVideoResponse>(aiApiUrl(config, path), payload, { headers: aiHeaders(config, "application/json") })).data);
        const id = readVideoTaskId(created);
        if (!id) throw new Error("视频接口没有返回任务 ID");
        return { id, provider: "openai", model: requestModel };
    } catch (error) {
        throw new Error(readAxiosError(error, "Grok 1.5 preview 视频任务创建失败"));
    }
}

async function createGrokVideoTask(config: AiConfig, model: string, prompt: string, references: ReferenceImage[], videoReferences: ReferenceVideo[], audioReferences: ReferenceAudio[]): Promise<VideoGenerationTask> {
    const path = videoCreatePath(config, model);
    if (path !== "/videos/generations") return createLegacyGrokVideoTask(config, model, prompt, references, videoReferences, audioReferences);
    assertGrokVideoReferences(videoReferences, audioReferences);
    const seconds = Number(normalizeVideoSeconds(config.videoSeconds));
    const payload = {
        model,
        prompt: buildGrokVideoPromptText(prompt, references),
        duration: seconds,
        aspect_ratio: normalizeGrokVideoAspectRatio(config.size || normalizeGrokVideoSize(config.size)),
        resolution: normalizeGrokVideoResolution(config.vquality),
        ...(references.length ? { reference_images: await buildGrokImageReferences(config, references) } : {}),
    };

    try {
        const created = unwrapVideoResponse((await axios.post<ApiVideoResponse>(aiApiUrl(config, path), payload, { headers: aiHeaders(config, "application/json") })).data);
        const id = readVideoTaskId(created);
        if (!id) throw new Error("视频接口没有返回任务 ID");
        return { id, provider: "openai", model };
    } catch (error) {
        throw new Error(readAxiosError(error, "Grok 视频任务创建失败"));
    }
}

async function createLegacyGrokVideoTask(config: AiConfig, model: string, prompt: string, references: ReferenceImage[], videoReferences: ReferenceVideo[], audioReferences: ReferenceAudio[]): Promise<VideoGenerationTask> {
    assertGrokLegacyReferences(references, videoReferences, audioReferences);
    const seconds = Number(normalizeVideoSeconds(config.videoSeconds));
    const size = normalizeGrokVideoSize(config.size);
    const aspectRatio = normalizeGrokVideoAspectRatio(config.size || size);
    const resolution = normalizeVideoResolution(config.vquality);
    const videoConfig = {
        seconds,
        duration: seconds,
        size,
        aspect_ratio: aspectRatio,
        resolution,
        resolution_name: resolution,
    };
    const payload = {
        model,
        stream: false,
        messages: [{ role: "user", content: [{ type: "text", text: prompt.trim() }] }],
        duration: seconds,
        seconds,
        aspect_ratio: aspectRatio,
        size,
        video_config: videoConfig,
        metadata: { video_config: videoConfig },
    };

    try {
        const created = unwrapVideoResponse((await axios.post<ApiVideoResponse>(aiApiUrl(config, "/videos"), payload, { headers: aiHeaders(config, "application/json") })).data);
        const id = readVideoTaskId(created);
        if (!id) throw new Error("视频接口没有返回任务 ID");
        return { id, provider: "openai", model };
    } catch (error) {
        throw new Error(readAxiosError(error, "Grok 视频任务创建失败"));
    }
}

async function createOpenAIVideoTask(config: AiConfig, model: string, prompt: string, references: ReferenceImage[], videoReferences: ReferenceVideo[], audioReferences: ReferenceAudio[]): Promise<VideoGenerationTask> {
    const path = openAIVideoCreatePath(model);
    const seedance = isSeedanceVideoModel(model);
    const seedanceRatio = seedance ? normalizeSeedanceRatio(config.size) : "";
    const seedanceResolution = seedance ? normalizeSeedanceResolution(config.vquality, model) : "";
    const seedanceDuration = seedance ? normalizeSeedanceDuration(config.videoSeconds) : 0;
    const body = new FormData();
    body.append("model", model);
    body.append("prompt", prompt);
    body.append("seconds", seedance ? String(seedanceDuration === -1 ? 6 : seedanceDuration) : normalizeVideoSeconds(config.videoSeconds));
    const size = seedance ? seedanceVideoSize(seedanceResolution, seedanceRatio) : normalizeVideoSize(config.size);
    if (size) body.append("size", size);
    body.append("resolution_name", normalizeVideoResolution(config.vquality));
    body.append("preset", "normal");
    if (seedance) {
        body.append("ratio", seedanceRatio);
        body.append("resolution", seedanceResolution);
        body.append("duration", String(seedanceDuration));
        body.append("generate_audio", String(boolConfig(config.videoGenerateAudio, true)));
        body.append("watermark", String(boolConfig(config.videoWatermark, false)));
    }
    const files = await Promise.all(references.slice(0, seedance ? SEEDANCE_REFERENCE_LIMITS.images : 7).map(async (image) => dataUrlToFile({ ...image, dataUrl: await imageToDataUrl(image) })));
    files.forEach((file) => body.append("input_reference[]", file));
    const videoFiles = await Promise.all(videoReferences.slice(0, SEEDANCE_REFERENCE_LIMITS.videos).map(referenceVideoToFile));
    videoFiles.forEach((file) => body.append("input_reference[]", file));
    const audioFiles = await Promise.all(audioReferences.slice(0, SEEDANCE_REFERENCE_LIMITS.audios).map(referenceAudioToFile));
    audioFiles.forEach((file) => body.append("input_reference[]", file));
    try {
        const created = unwrapVideoResponse((await axios.post<ApiVideoResponse>(aiApiUrl(config, path), body, { headers: aiHeaders(config) })).data);
        const id = readVideoTaskId(created);
        if (!id) throw new Error("视频接口没有返回任务 ID");
        return { id, provider: "openai", model, path };
    } catch (error) {
        throw new Error(readAxiosError(error, "视频任务创建失败"));
    }
}

async function referenceVideoToFile(video: ReferenceVideo) {
    const blob = await referenceMediaBlob(video.storageKey, video.url);
    if (!blob) throw new Error("参考视频读取失败，请重新上传或使用公网视频 URL");
    return new File([blob], video.name || "reference-video.mp4", { type: video.type || blob.type || "video/mp4" });
}

async function referenceAudioToFile(audio: ReferenceAudio) {
    const blob = await referenceMediaBlob(audio.storageKey, audio.url);
    if (!blob) throw new Error("参考音频读取失败，请重新上传或使用公网音频 URL");
    return new File([blob], audio.name || "reference-audio.mp3", { type: audio.type || blob.type || "audio/mpeg" });
}

async function referenceMediaBlob(storageKey: string | undefined, url: string) {
    if (storageKey) {
        const stored = await getMediaBlob(storageKey);
        if (stored) return stored;
    }
    if (!url || url.startsWith("asset://")) return null;
    try {
        return await (await fetch(url)).blob();
    } catch {
        return null;
    }
}

async function resolveGrokImageUrl(config: AiConfig, image: ReferenceImage) {
    const directUrl = image.url || image.dataUrl;
    if (isPublicMediaUrl(directUrl)) return directUrl;
    const dataUrl = await imageToDataUrl(image);
    if (!dataUrl?.startsWith("data:image/")) throw new Error("参考图读取失败，请换一张图片或重新上传");
    if (config.channelMode === "remote") return uploadReferenceMedia(dataUrlToFile({ ...image, dataUrl }));
    return dataUrl;
}

async function buildGrokImageReferences(config: AiConfig, references: ReferenceImage[]) {
    return Promise.all(references.slice(0, 4).map((image) => resolveGrokImageUrl(config, image)));
}

async function pollOpenAIVideoTask(config: AiConfig, task: VideoGenerationTask): Promise<VideoGenerationTaskState> {
    const path = task.path || openAIVideoCreatePath(task.model);
    try {
        const video = unwrapVideoResponse((await axios.get<ApiVideoResponse>(aiApiUrl(config, `${path}/${task.id}`), { headers: aiHeaders(config), params: config.channelMode === "remote" ? { model: task.model } : undefined })).data);
        const status = readVideoStatus(video);
        const errorMessage = readVideoErrorMessage(video);
        if (isFailedVideoStatus(status) || (errorMessage && !isCompletedVideoStatus(status))) {
            return { status: "failed", error: errorMessage || (status === "expired" ? "视频生成任务已过期" : "视频生成失败") };
        }
        const videoUrl = readVideoUrl(video);
        if (videoUrl && (!status || isCompletedVideoStatus(status))) {
            refreshRemoteUser(config);
            return { status: "completed", result: await videoResultFromUrl(videoUrl, config, task) };
        }
        if (isCompletedVideoStatus(status)) {
            const content = await axios.get<Blob>(aiApiUrl(config, `${path}/${task.id}/content`), { headers: aiHeaders(config), params: config.channelMode === "remote" ? { model: task.model } : undefined, responseType: "blob" });
            await assertVideoBlob(content.data);
            refreshRemoteUser(config);
            return { status: "completed", result: { blob: content.data } };
        }
        return { status: "pending" };
    } catch (error) {
        throw new Error(readAxiosError(error, "视频任务查询失败"));
    }
}

async function createSeedanceTask(config: AiConfig, model: string, prompt: string, references: ReferenceImage[], videoReferences: ReferenceVideo[], audioReferences: ReferenceAudio[]): Promise<VideoGenerationTask> {
    if (audioReferences.length && !references.length && !videoReferences.length) {
        throw new Error("Seedance 参考音频不能单独使用，请同时添加参考图或参考视频");
    }
    assertSeedanceVideoReferences(videoReferences);
    assertSeedanceAudioReferences(audioReferences);
    const content = await buildSeedanceContent(config, prompt, references, videoReferences, audioReferences);
    if (!content.length) throw new Error("请输入视频提示词，或连接参考图片/视频/音频");
    const payload = {
        model,
        content,
        ratio: normalizeSeedanceRatio(config.size),
        resolution: normalizeSeedanceResolution(config.vquality, model),
        duration: normalizeSeedanceDuration(config.videoSeconds),
        generate_audio: boolConfig(config.videoGenerateAudio, true),
        watermark: boolConfig(config.videoWatermark, false),
    };

    try {
        const created = unwrapSeedanceTask((await axios.post<ApiEnvelope<SeedanceTask>>(seedanceApiUrl(config), payload, { headers: aiHeaders(config, "application/json") })).data);
        if (!created.id) throw new Error("Seedance 接口没有返回任务 ID");
        return { id: created.id, provider: "seedance", model };
    } catch (error) {
        throw new Error(readAxiosError(error, "Seedance 任务创建失败"));
    }
}

async function pollSeedanceTask(config: AiConfig, task: VideoGenerationTask): Promise<VideoGenerationTaskState> {
    try {
        const state = unwrapSeedanceTask((await axios.get<ApiEnvelope<SeedanceTask>>(seedanceApiUrl(config, task.id), { headers: aiHeaders(config), params: config.channelMode === "remote" ? { model: task.model } : undefined })).data);
        if (state.status === "succeeded") {
            const url = state.content?.video_url;
            if (!url) return { status: "failed", error: "Seedance 任务成功但没有返回视频 URL" };
            refreshRemoteUser(config);
            return { status: "completed", result: await videoResultFromUrl(url, config, task) };
        }
        if (state.status === "failed" || state.status === "cancelled" || state.status === "expired") return { status: "failed", error: state.error?.message || `Seedance 视频生成${state.status === "expired" ? "超时" : "失败"}` };
        return { status: "pending" };
    } catch (error) {
        throw new Error(readAxiosError(error, "Seedance 任务查询失败"));
    }
}

function assertSeedanceVideoReferences(videoReferences: ReferenceVideo[]) {
    const error = seedanceVideoReferenceError(videoReferences);
    if (error) throw new Error(error);
    let total = 0;
    for (const video of videoReferences) {
        if (!video.durationMs) continue;
        if (video.durationMs < 2000 || video.durationMs > 15000) throw new Error("Seedance 参考视频单个时长需要在 2-15 秒之间");
        total += video.durationMs;
    }
    if (total > 15000) throw new Error("Seedance 参考视频总时长不能超过 15 秒");
}

function assertSeedanceAudioReferences(audioReferences: ReferenceAudio[]) {
    let total = 0;
    for (const audio of audioReferences) {
        if (!audio.durationMs) continue;
        if (audio.durationMs < 2000 || audio.durationMs > 15000) throw new Error("Seedance 参考音频单个时长需要在 2-15 秒之间");
        total += audio.durationMs;
    }
    if (total > 15000) throw new Error("Seedance 参考音频总时长不能超过 15 秒");
}

function seedanceApiUrl(config: AiConfig, taskId?: string) {
    if (config.channelMode === "remote") return taskId ? `/api/v1/videos/${encodeURIComponent(taskId)}` : "/api/v1/videos";
    return buildApiUrl(config.baseUrl, `/contents/generations/tasks${taskId ? `/${encodeURIComponent(taskId)}` : ""}`);
}

async function buildSeedanceContent(config: AiConfig, prompt: string, references: ReferenceImage[], videoReferences: ReferenceVideo[], audioReferences: ReferenceAudio[]) {
    const content: Array<Record<string, unknown>> = [];
    const text = buildSeedancePromptText(prompt, references, videoReferences, audioReferences);
    if (text) content.push({ type: "text", text });
    for (const image of references.slice(0, SEEDANCE_REFERENCE_LIMITS.images)) {
        content.push({ type: "image_url", image_url: { url: await resolveSeedanceImageUrl(config, image) }, role: "reference_image" });
    }
    for (const video of videoReferences.slice(0, SEEDANCE_REFERENCE_LIMITS.videos)) {
        content.push({ type: "video_url", video_url: { url: await resolveSeedanceVideoUrl(video) }, role: "reference_video" });
    }
    for (const audio of audioReferences.slice(0, SEEDANCE_REFERENCE_LIMITS.audios)) {
        content.push({ type: "audio_url", audio_url: { url: await resolveSeedanceAudioUrl(audio) }, role: "reference_audio" });
    }
    return content;
}

async function resolveSeedanceImageUrl(config: AiConfig, image: ReferenceImage) {
    const directUrl = image.url || image.dataUrl;
    if (isPublicMediaUrl(directUrl) || directUrl.startsWith("asset://")) return directUrl;
    const dataUrl = await imageToDataUrl(image);
    if (!dataUrl) throw new Error("参考图读取失败，请换一张图片或重新上传");
    if (config.channelMode === "remote") {
        return uploadReferenceMedia(dataUrlToFile({ ...image, dataUrl }));
    }
    return dataUrl;
}

async function resolveSeedanceVideoUrl(video: ReferenceVideo) {
    if (isPublicMediaUrl(video.url) || video.url.startsWith("asset://")) return video.url;
    let blob: Blob | null = null;
    if (video.storageKey) blob = await getMediaBlob(video.storageKey);
    if (!blob && video.url?.startsWith("blob:")) blob = await (await fetch(video.url)).blob();
    if (!blob) throw new Error("参考视频必须是公网 URL、素材 ID，或本地已保存的视频");
    const file = new File([blob], video.name || "reference-video.mp4", { type: video.type || blob.type || "video/mp4" });
    return uploadReferenceMedia(file);
}

async function resolveSeedanceAudioUrl(audio: ReferenceAudio) {
    if (isPublicMediaUrl(audio.url) || audio.url.startsWith("asset://")) return audio.url;
    let blob: Blob | null = null;
    if (audio.storageKey) blob = await getMediaBlob(audio.storageKey);
    if (!blob && audio.url?.startsWith("blob:")) blob = await (await fetch(audio.url)).blob();
    if (!blob) throw new Error("参考音频必须是公网 URL、素材 ID，或本地已保存的音频");
    const file = new File([blob], audio.name || "reference-audio.mp3", { type: audio.type || blob.type || "audio/mpeg" });
    return uploadReferenceMedia(file);
}

async function uploadReferenceMedia(file: File) {
    const token = useUserStore.getState().token;
    if (!token) throw new Error("使用本地参考素材需要先登录，并在服务端配置 PUBLIC_BASE_URL");
    const body = new FormData();
    body.append("file", file, file.name);
    const response = await axios.post<ApiEnvelope<ReferenceMediaUploadResponse>>("/api/v1/media/references", body, { headers: { Authorization: `Bearer ${token}` } });
    const payload = unwrapEnvelope(response.data, "参考素材上传失败");
    if (!payload.url) throw new Error("参考素材上传后没有返回公网 URL");
    return payload.url;
}

async function videoResultFromUrl(url: string, config?: AiConfig, task?: VideoGenerationTask): Promise<VideoGenerationResult> {
    const remoteContentUrl = remoteVideoContentProxyUrl(config, url);
    const resolvedUrl = resolveVideoUrl(config, url);
    try {
        if (remoteContentUrl) {
            const response = await axios.get<Blob>(remoteContentUrl, { headers: aiHeaders(config!), params: task?.model ? { model: task.model } : undefined, responseType: "blob" });
            await assertVideoBlob(response.data);
            return { blob: response.data };
        }
        if (config?.channelMode === "local" && shouldSendVideoContentAuth(config, resolvedUrl)) {
            const response = await axios.post<Blob>("/api/video-content", { url: resolvedUrl, apiKey: config.apiKey }, { responseType: "blob" });
            await assertVideoBlob(response.data);
            return { blob: response.data };
        }
        const headers = config && shouldSendVideoContentAuth(config, resolvedUrl) ? aiHeaders(config) : undefined;
        const response = await axios.get<Blob>(resolvedUrl, { responseType: "blob", ...(headers ? { headers } : {}) });
        await assertVideoBlob(response.data);
        return { blob: response.data };
    } catch (error) {
        if (remoteContentUrl) throw new Error(readAxiosError(error, "视频已生成，但下载视频内容失败"));
        return { url: resolvedUrl, mimeType: "video/mp4" };
    }
}

function resolveVideoUrl(config: AiConfig | undefined, value: string) {
    const text = String(value || "").trim();
    if (!config || config.channelMode === "remote" || !text || /^(?:https?:|data:|blob:|asset:|\/\/)/i.test(text)) return text;
    const path = text.replace(/^\/v1(?=\/)/i, "");
    return buildApiUrl(config.baseUrl, path);
}

function remoteVideoContentProxyUrl(config: AiConfig | undefined, url: string) {
    if (config?.channelMode !== "remote") return "";
    const taskId = videoContentTaskId(url);
    if (!taskId) return "";
    const path = videoContentPath(url).match(/\/(?:(?:api\/)?v1\/)?video\/generations\//i) ? "/video/generations" : "/videos";
    return aiApiUrl(config, `${path}/${encodeURIComponent(taskId)}/content`);
}

function videoContentTaskId(value: string) {
    const path = videoContentPath(value);
    const id = path.match(/\/(?:(?:api\/)?v1\/)?video(?:s|\/generations)\/([^/]+)\/content\/?$/i)?.[1] || "";
    try {
        return decodeURIComponent(id);
    } catch {
        return id;
    }
}

function videoContentPath(value: string) {
    const text = String(value || "").trim();
    if (!text) return "";
    try {
        if (/^(https?:)?\/\//i.test(text)) return new URL(text.startsWith("//") ? `https:${text}` : text).pathname;
    } catch {
        return "";
    }
    return text.split(/[?#]/)[0];
}

function shouldSendVideoContentAuth(config: AiConfig, url: string) {
    const videosBaseUrl = aiApiUrl(config, "/videos/");
    const legacyVideosBaseUrl = aiApiUrl(config, "/video/generations/");
    return (url.startsWith(videosBaseUrl) || url.startsWith(legacyVideosBaseUrl)) && /\/content(?:\?|$)/.test(url);
}

function assertVideoConfig(config: AiConfig, model: string) {
    if (!model) throw new Error("请先配置视频模型");
    if (config.channelMode === "local" && !config.baseUrl.trim()) throw new Error("请先配置 Base URL");
    if (config.channelMode === "local" && !config.apiKey.trim()) throw new Error("请先配置 API Key");
}

function normalizeVideoSeconds(value: string) {
    const seconds = Math.floor(Number(value) || 6);
    return String(Math.max(1, Math.min(20, seconds)));
}

function normalizeVideoSize(value: string) {
    if (value === "auto") return null;
    const size = value || "1280x720";
    if (/^\d+x\d+$/.test(size)) return size;
    return VIDEO_RATIO_SIZES[size] || "1280x720";
}

function normalizeVideoResolution(value: string) {
    const normalized = String(value || "").trim().toLowerCase();
    if (normalized === "low") return "480p";
    if (normalized === "auto" || normalized === "high" || normalized === "medium" || normalized === "hd") return "720p";
    if (normalized === "fhd") return "1080p";
    if (normalized === "2k" || normalized === "qhd") return "1440p";
    if (normalized === "4k" || normalized === "uhd") return "2160p";
    const resolution = normalized.replace(/p$/i, "") || "720";
    if (!/^\d+$/.test(resolution)) return "720p";
    return `${resolution}p`;
}

function normalizeGrokVideoResolution(value: string) {
    const resolution = normalizeVideoResolution(value);
    return resolution === "480p" ? "480p" : "720p";
}

function buildGrokVideoPromptText(prompt: string, references: ReferenceImage[]) {
    const text = prompt.trim();
    if (!references.length) return text;
    const labels = references.slice(0, 7).map((_, index) => `<IMAGE_${index + 1}>`).join("、");
    return `参考图片编号：${labels}。\n\n${text}`;
}

function assertGrokPreviewReferences(references: ReferenceImage[], videoReferences: ReferenceVideo[], audioReferences: ReferenceAudio[]) {
    if (videoReferences.length || audioReferences.length) throw new Error("Grok 1.5 preview 只支持首帧图片，不支持参考视频或参考音频");
    if (!references.length) throw new Error("Grok 1.5 preview 只支持首帧生视频，请先添加 1 张首帧图片");
    if (references.length > 1) throw new Error("Grok 1.5 preview 只支持 1 张首帧图片，请只保留一张参考图");
}

function assertGrokVideoReferences(videoReferences: ReferenceVideo[], audioReferences: ReferenceAudio[]) {
    if (!videoReferences.length && !audioReferences.length) return;
    throw new Error("xAI Grok Imagine Video 官方接口当前只支持文本和参考图片；参考视频或参考音频请切换到 Seedance 2.0 / 火山 Agent Plan 模型");
}

function assertGrokLegacyReferences(references: ReferenceImage[], videoReferences: ReferenceVideo[], audioReferences: ReferenceAudio[]) {
    if (!references.length && !videoReferences.length && !audioReferences.length) return;
    throw new Error("当前 Grok 兼容网关按非流文生视频接口发送，不支持参考素材；xAI 官方参考图视频请把 Base URL 配置为 https://api.x.ai");
}

function unwrapVideoResponse(payload: ApiVideoResponse) {
    return unwrapEnvelope(payload, "接口没有返回视频任务");
}

function unwrapSeedanceTask(payload: ApiEnvelope<SeedanceTask>) {
    return unwrapEnvelope(payload, "Seedance 接口没有返回任务");
}

function isCompletedVideoStatus(status: string) {
    return status === "done" || status === "completed" || status === "complete" || status === "succeeded" || status === "success";
}

function isFailedVideoStatus(status: string) {
    return status === "failed" || status === "failure" || status === "error" || status === "expired" || status === "cancelled" || status === "canceled";
}

function unwrapEnvelope<T>(payload: ApiEnvelope<T>, emptyMessage: string): T {
    if (!payload) throw new Error(emptyMessage);
    if (typeof payload === "object" && "code" in payload && typeof payload.code === "number") {
        if (payload.code !== 0) throw new Error(payload.msg || "请求失败");
        if (!payload.data) throw new Error(emptyMessage);
        return payload.data;
    }
    return payload as T;
}

function readAxiosError(error: unknown, fallback: string) {
    if (axios.isAxiosError<unknown>(error)) {
        const responseData = error.response?.data;
        return normalizeVideoErrorMessage(errorResponseMessage(responseData) || statusMessage(error.response?.status, fallback));
    }
    return normalizeVideoErrorMessage(error instanceof Error ? error.message : fallback);
}

function errorResponseMessage(value: unknown) {
    if (typeof value === "string") return value;
    if (!value || typeof value !== "object") return "";
    const payload = value as { error?: string | { message?: unknown }; message?: unknown; msg?: unknown; detail?: unknown };
    if (typeof payload.error === "string") return payload.error;
    if (payload.error && typeof payload.error === "object" && typeof payload.error.message === "string") return payload.error.message;
    if (typeof payload.detail === "string") return payload.detail;
    if (typeof payload.msg === "string") return payload.msg;
    return typeof payload.message === "string" ? payload.message : "";
}

function statusMessage(status: number | undefined, fallback: string) {
    if (status === 401 || status === 403) return "鉴权失败，请检查 API Key、套餐权限或模型权限";
    if (status === 429) return "请求被限流或额度不足，请稍后重试";
    return status ? `${fallback}（${status}）` : fallback;
}

async function assertVideoBlob(blob: Blob) {
    if (!blob.type.includes("json")) return;
    let payload: { code?: number; msg?: string; error?: string | { message?: string } };
    try {
        payload = JSON.parse(await blob.text()) as { code?: number; msg?: string; error?: { message?: string } };
    } catch {
        return;
    }
    if (typeof payload.code === "number" && payload.code !== 0) throw new Error(payload.msg || "视频下载失败");
    if (typeof payload.error === "string") throw new Error(payload.error);
    if (payload.error && typeof payload.error === "object" && payload.error.message) throw new Error(payload.error.message);
}

function normalizeVideoErrorMessage(message: string) {
    let text = trimErrorText(message);
    for (let index = 0; index < 5; index += 1) {
        const next = nestedJSONMessage(text);
        if (!next || next === text) break;
        text = trimErrorText(next);
    }
    if (/requires an input image|text-to-video is not supported/i.test(text)) {
        return `${GROK_IMAGE_VIDEO_MODEL} 只支持首帧生视频，请先添加 1 张首帧图片；纯文本生视频请切换到 grok-imagine-video。`;
    }
    return text;
}

function nestedJSONMessage(text: string) {
    const value = trimErrorText(text);
    if (!value.startsWith("{") || !value.endsWith("}")) return "";
    try {
        const payload = JSON.parse(value) as { msg?: string; message?: string; error?: { message?: string } };
        return payload.error?.message || payload.message || payload.msg || "";
    } catch {
        return "";
    }
}

function trimErrorText(value: string) {
    return String(value || "").trim();
}

function isPublicMediaUrl(value: string) {
    return /^https?:\/\//i.test(value || "");
}

function openAIVideoCreatePath(model: string) {
    return /^seedance-2-0(?:-fast)?$/i.test(model.trim()) ? "/video/generations" : "/videos";
}

function seedanceVideoSize(resolution: string, ratio: string) {
    const size = seedancePixelLabel(resolution, ratio);
    return /^\d+x\d+$/.test(size) ? size : null;
}

function isVideoToVideoModel(model: string) {
    return /(?:v2v|video[-_ ]to[-_ ]video|video2video)/i.test(model);
}

function isXAIBaseUrl(baseUrl: string) {
    try {
        return new URL(baseUrl).hostname.toLowerCase().endsWith("x.ai");
    } catch {
        return /(^|\.)x\.ai(?:\/|$)/i.test(baseUrl);
    }
}

function delay(ms: number) {
    return new Promise((resolve) => setTimeout(resolve, ms));
}
