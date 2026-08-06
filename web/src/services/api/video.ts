import axios from "axios";
import { nanoid } from "nanoid";

import i18n from "@/i18n";
import { GROK_IMAGE_VIDEO_MODEL, isGrokImagineVideoModel, isGrokPreviewVideoModel, normalizeGrokPreviewModel, normalizeGrokPreviewSeconds, normalizeGrokPreviewVideoSize, normalizeGrokVideoAspectRatio, normalizeGrokVideoSize } from "@/lib/grok-video";
import { dataUrlToFile } from "@/lib/image-utils";
import { getMediaBlob, uploadMediaFile, type UploadedFile } from "@/services/file-storage";
import { imageToDataUrl } from "@/services/image-storage";
import { boolConfig, buildSeedancePromptText, isSeedanceVideoConfig, normalizeSeedanceDuration, normalizeSeedanceRatio, normalizeSeedanceResolution, seedanceVideoReferenceError, SEEDANCE_REFERENCE_LIMITS } from "@/lib/seedance-video";
import { readVideoErrorMessage, readVideoStatus, readVideoTaskId, readVideoUrl, type VideoResponse } from "@/services/api/video-response";
import { isGatewayModel } from "@/services/gateway-channel";
import { buildApiUrl, modelOptionName, resolveModelRequestConfig, resolveModelScript, type AiConfig } from "@/stores/use-config-store";
import { useUserStore } from "@/stores/use-user-store";
import { runModelPlugin } from "./model-plugin";
import type { ReferenceImage } from "@/types/image";
import type { ReferenceAudio, ReferenceVideo } from "@/types/media";

type ApiVideoResponse = VideoResponse | { code?: number; data?: VideoResponse | null; msg?: string };
type SeedanceTask = {
    id: string;
    status?: "queued" | "running" | "succeeded" | "completed" | "failed" | "cancelled" | "expired";
    error?: { code?: string; message?: string } | null;
    content?: { video_url?: string; url?: string; last_frame_url?: string } | null;
};
type ApiEnvelope<T> = T | { code?: number | string; data?: T | null; msg?: string };
type ReferenceMediaUploadResponse = { id: string; url: string; mimeType: string; bytes: number };
type RequestOptions = { signal?: AbortSignal };
const apiText = (key: string, options?: Record<string, unknown>) => i18n.t(`apiErrors.${key}`, options);

export type VideoGenerationResult = { blob?: Blob; url?: string; mimeType?: string };
/** `model` 保留渠道前缀用于重新解析渠道；`requestModel` 是实际发给服务商的模型名。 */
export type VideoGenerationTask = { id: string; provider: "openai" | "seedance" | "plugin"; model: string; requestModel?: string; path?: string };
export type VideoGenerationTaskState = { status: "pending" } | { status: "completed"; result: VideoGenerationResult } | { status: "failed"; error: string };

/** Results for scripted (plugin) video models, which run their own create+poll in one shot at task creation. */
const pluginVideoResults = new Map<string, VideoGenerationResult>();

function aiApiUrl(config: AiConfig, path: string) {
    return buildApiUrl(config.baseUrl, path);
}

function aiHeaders(config: AiConfig, contentType?: string) {
    return {
        Authorization: `Bearer ${config.apiKey}`,
        ...(contentType ? { "Content-Type": contentType } : {}),
    };
}

/** 走内置网关时后端会扣算力点，生成完成后刷新余额。 */
function refreshGatewayUser(remote: boolean) {
    if (remote) void useUserStore.getState().hydrateUser();
}

/** 网关和 xAI 官方都走 /videos/generations；其它兼容网关仍是 /videos。 */
export function videoCreatePath(config: AiConfig, model: string, remote: boolean) {
    return isGrokImagineVideoModel(model) && (remote || isXAIBaseUrl(config.baseUrl)) ? "/videos/generations" : "/videos";
}

/** 兼容网关上的 seedance / veo / omni 都收 JSON 版 /videos；官方 Sora 仍走 multipart。 */
export function isUnifiedJsonVideoModel(model: string) {
    const value = model.toLowerCase();
    if (value.includes("doubao-seedance")) return false;
    return value.includes("seedance") || value.includes("veo-") || value.includes("omni-");
}

export async function requestVideoGeneration(config: AiConfig, prompt: string, references: ReferenceImage[] = [], videoReferences: ReferenceVideo[] = [], audioReferences: ReferenceAudio[] = [], options?: RequestOptions): Promise<VideoGenerationResult> {
    const task = await createVideoGenerationTask(config, prompt, references, videoReferences, audioReferences, options);
    const delayMs = task.provider === "seedance" ? 5000 : 2500;
    for (let attempt = 0; attempt < 120; attempt += 1) {
        if (options?.signal?.aborted) throw new DOMException("Aborted", "AbortError");
        const state = await pollVideoGenerationTask(config, task, options);
        if (state.status === "completed") return state.result;
        if (state.status === "failed") throw new Error(state.error);
        if (attempt === 119) throw new Error(apiText("videoTimeout", { provider: task.provider === "seedance" ? "Seedance " : "" }));
        await delay(delayMs, options?.signal);
    }
    throw new Error(apiText("videoTimeout", { provider: "" }));
}

export async function createVideoGenerationTask(config: AiConfig, prompt: string, references: ReferenceImage[] = [], videoReferences: ReferenceVideo[] = [], audioReferences: ReferenceAudio[] = [], options?: RequestOptions): Promise<VideoGenerationTask> {
    const selectedModel = (config.model || config.videoModel).trim();
    const requestConfig = resolveModelRequestConfig(config, selectedModel);
    const script = resolveModelScript(config, selectedModel);
    if (script) return createPluginVideoTask(requestConfig, selectedModel, script, prompt, references, options);
    assertVideoConfig(requestConfig, requestConfig.model);
    const remote = isGatewayModel(config, selectedModel);
    const model = requestConfig.model;
    const isGrokVideo = isGrokImagineVideoModel(model);
    const isOpenAIVideoJson = isGrokVideo || isUnifiedJsonVideoModel(model);
    if (isSeedanceVideoConfig(requestConfig)) {
        return createSeedanceTask(requestConfig, selectedModel, remote, prompt, references, videoReferences, audioReferences, options);
    }
    // 只有统一 JSON 端点的模型收参考视频 / 音频，其余仍然只支持参考图
    if ((videoReferences.length || audioReferences.length) && !isOpenAIVideoJson) {
        throw new Error(apiText("videoReferencesUnsupported"));
    }
    if (isGrokPreviewVideoModel(model)) return createGrokPreviewVideoTask(requestConfig, selectedModel, remote, prompt, references, videoReferences, audioReferences, options);
    if (isGrokVideo) return createGrokVideoTask(requestConfig, selectedModel, remote, prompt, references, videoReferences, audioReferences, options);
    if (isOpenAIVideoJson) return createUnifiedVideoTask(requestConfig, selectedModel, remote, prompt, references, videoReferences, audioReferences, options);
    return createOpenAIVideoTask(requestConfig, selectedModel, prompt, references, videoReferences, audioReferences, options);
}

export async function pollVideoGenerationTask(config: AiConfig, task: VideoGenerationTask, options?: RequestOptions): Promise<VideoGenerationTaskState> {
    if (task.provider === "plugin") {
        const result = pluginVideoResults.get(task.id);
        return result ? { status: "completed", result } : { status: "failed", error: apiText("pluginVideoExpired") };
    }
    const requestConfig = resolveModelRequestConfig(config, task.model);
    assertVideoConfig(requestConfig, requestConfig.model);
    const remote = isGatewayModel(config, task.model);
    return task.provider === "seedance" ? pollSeedanceTask(requestConfig, task, remote, options) : pollOpenAIVideoTask(requestConfig, task, remote, options);
}

async function createPluginVideoTask(config: AiConfig, model: string, script: string, prompt: string, references: ReferenceImage[], options?: RequestOptions): Promise<VideoGenerationTask> {
    if (!config.baseUrl.trim()) throw new Error(apiText("baseUrlRequired"));
    if (!config.apiKey.trim()) throw new Error(apiText("apiKeyRequired"));
    const refs = await Promise.all(references.map((image) => imageToDataUrl(image)));
    const result = videoPluginResult(
        await runModelPlugin({
            capability: "video",
            script,
            config,
            prompt,
            images: refs,
            params: {
                seconds: normalizeVideoSeconds(config.videoSeconds),
                size: normalizeVideoSize(config.size),
                resolution: normalizeVideoResolution(config.vquality),
                ratio: config.size,
                generateAudio: boolConfig(config.videoGenerateAudio, true),
                watermark: boolConfig(config.videoWatermark, false),
            },
            signal: options?.signal,
        }),
    );
    const id = nanoid();
    pluginVideoResults.set(id, result);
    return { id, provider: "plugin", model };
}

function videoPluginResult(result: unknown): VideoGenerationResult {
    if (result instanceof Blob) return { blob: result };
    if (typeof result === "string") return { url: result, mimeType: "video/mp4" };
    if (result && typeof result === "object") {
        const record = result as Record<string, unknown>;
        if (record.blob instanceof Blob) return { blob: record.blob };
        const url = [record.url, record.video_url, record.result_url].find((value) => typeof value === "string" && value) as string | undefined;
        if (url) return { url, mimeType: "video/mp4" };
    }
    throw new Error(apiText("scriptNoVideo"));
}

export async function storeGeneratedVideo(result: VideoGenerationResult): Promise<UploadedFile> {
    if (result.blob) return uploadMediaFile(result.blob, "video");
    if (result.url) {
        try {
            return await uploadMediaFile(result.url, "video");
        } catch {
            return { url: result.url, storageKey: "", bytes: 0, mimeType: result.mimeType || "video/mp4" };
        }
    }
    throw new Error(apiText("noPlayableVideo"));
}

async function createGrokPreviewVideoTask(config: AiConfig, selectedModel: string, remote: boolean, prompt: string, references: ReferenceImage[], videoReferences: ReferenceVideo[], audioReferences: ReferenceAudio[], options?: RequestOptions): Promise<VideoGenerationTask> {
    assertGrokPreviewReferences(references, videoReferences, audioReferences);
    const model = config.model;
    const path = videoCreatePath(config, model, remote);
    const official = path === "/videos/generations";
    const requestModel = official && !remote ? normalizeGrokPreviewModel(model) : model;
    const firstFrame = official ? await resolveGrokImageUrl(config, remote, references[0]) : await imageToDataUrl(references[0]);
    if (!firstFrame || (!firstFrame.startsWith("data:image/") && !isPublicMediaUrl(firstFrame))) throw new Error("首帧图片读取失败，请重新上传图片");
    const duration = Number(normalizeGrokPreviewSeconds(config.videoSeconds, model));
    const aspectRatio = normalizeGrokVideoAspectRatio(config.size || normalizeGrokPreviewVideoSize(config.size));
    const payload = official
        ? {
              model: requestModel,
              // 网关模式由后端按渠道决定是否保留 prompt；本地直连 xAI 官方接口不发 prompt
              ...(remote && prompt.trim() ? { prompt: prompt.trim() } : {}),
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
        const created = unwrapVideoResponse((await axios.post<ApiVideoResponse>(aiApiUrl(config, path), payload, { headers: aiHeaders(config, "application/json"), signal: options?.signal })).data);
        const id = readVideoTaskId(created);
        if (!id) throw new Error("视频接口没有返回任务 ID");
        return { id, provider: "openai", model: selectedModel, requestModel };
    } catch (error) {
        throw new Error(readAxiosError(error, "Grok 1.5 preview 视频任务创建失败"));
    }
}

async function createGrokVideoTask(config: AiConfig, selectedModel: string, remote: boolean, prompt: string, references: ReferenceImage[], videoReferences: ReferenceVideo[], audioReferences: ReferenceAudio[], options?: RequestOptions): Promise<VideoGenerationTask> {
    const model = config.model;
    const path = videoCreatePath(config, model, remote);
    if (path !== "/videos/generations") return createLegacyGrokVideoTask(config, selectedModel, prompt, references, videoReferences, audioReferences, options);
    assertGrokVideoReferences(videoReferences, audioReferences);
    const seconds = Number(normalizeVideoSeconds(config.videoSeconds));
    const payload = {
        model,
        prompt: buildGrokVideoPromptText(prompt, references),
        duration: seconds,
        aspect_ratio: normalizeGrokVideoAspectRatio(config.size || normalizeGrokVideoSize(config.size)),
        resolution: normalizeGrokVideoResolution(config.vquality),
        ...(references.length ? { reference_images: await buildGrokImageReferences(config, remote, references) } : {}),
    };

    try {
        const created = unwrapVideoResponse((await axios.post<ApiVideoResponse>(aiApiUrl(config, path), payload, { headers: aiHeaders(config, "application/json"), signal: options?.signal })).data);
        const id = readVideoTaskId(created);
        if (!id) throw new Error("视频接口没有返回任务 ID");
        return { id, provider: "openai", model: selectedModel, requestModel: model };
    } catch (error) {
        throw new Error(readAxiosError(error, "Grok 视频任务创建失败"));
    }
}

async function createLegacyGrokVideoTask(config: AiConfig, selectedModel: string, prompt: string, references: ReferenceImage[], videoReferences: ReferenceVideo[], audioReferences: ReferenceAudio[], options?: RequestOptions): Promise<VideoGenerationTask> {
    assertGrokLegacyReferences(references, videoReferences, audioReferences);
    const model = config.model;
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
        const created = unwrapVideoResponse((await axios.post<ApiVideoResponse>(aiApiUrl(config, "/videos"), payload, { headers: aiHeaders(config, "application/json"), signal: options?.signal })).data);
        const id = readVideoTaskId(created);
        if (!id) throw new Error("视频接口没有返回任务 ID");
        return { id, provider: "openai", model: selectedModel, requestModel: model };
    } catch (error) {
        throw new Error(readAxiosError(error, "Grok 视频任务创建失败"));
    }
}

/** SubRouter / new-api 风格网关把 seedance 放在 OpenAI 风格的 /videos 上，请求体是统一的 JSON。 */
async function createUnifiedVideoTask(config: AiConfig, selectedModel: string, remote: boolean, prompt: string, references: ReferenceImage[], videoReferences: ReferenceVideo[], audioReferences: ReferenceAudio[], options?: RequestOptions): Promise<VideoGenerationTask> {
    if (videoReferences.length || audioReferences.length) {
        throw new Error("当前渠道的 Seedance 模型暂不支持参考视频或参考音频，请移除后重试，或切换到火山方舟渠道");
    }
    const model = config.model;
    const path = "/videos";
    const duration = normalizeSeedanceDuration(config.videoSeconds);
    const images = await Promise.all(references.slice(0, SEEDANCE_REFERENCE_LIMITS.images).map((image) => resolveGrokImageUrl(config, remote, image)));
    const payload = {
        model,
        prompt,
        duration: duration === -1 ? 6 : duration,
        metadata: {
            ratio: normalizeSeedanceRatio(config.size),
            resolution: normalizeSeedanceResolution(config.vquality, model),
            generate_audio: boolConfig(config.videoGenerateAudio, true),
            watermark: boolConfig(config.videoWatermark, false),
        },
        ...(images.length ? { image: images[0] } : {}),
        ...(images.length > 1 ? { images } : {}),
    };
    try {
        const created = unwrapVideoResponse((await axios.post<ApiVideoResponse>(aiApiUrl(config, path), payload, { headers: aiHeaders(config, "application/json"), signal: options?.signal })).data);
        const id = readVideoTaskId(created);
        if (!id) throw new Error("视频接口没有返回任务 ID");
        return { id, provider: "openai", model: selectedModel, requestModel: model, path };
    } catch (error) {
        throw new Error(readAxiosError(error, "视频任务创建失败"));
    }
}

async function createOpenAIVideoTask(config: AiConfig, selectedModel: string, prompt: string, references: ReferenceImage[], videoReferences: ReferenceVideo[], audioReferences: ReferenceAudio[], options?: RequestOptions): Promise<VideoGenerationTask> {
    const model = config.model;
    const path = "/videos";
    const body = new FormData();
    body.append("model", model);
    body.append("prompt", prompt);
    body.append("seconds", normalizeVideoSeconds(config.videoSeconds));
    if (normalizeVideoSize(config.size)) body.append("size", normalizeVideoSize(config.size)!);
    body.append("resolution_name", normalizeVideoResolution(config.vquality));
    body.append("preset", "normal");
    const files = await Promise.all(references.slice(0, 7).map(async (image) => dataUrlToFile({ ...image, dataUrl: await imageToDataUrl(image) })));
    files.forEach((file) => body.append("input_reference[]", file));
    const videoFiles = await Promise.all(videoReferences.slice(0, SEEDANCE_REFERENCE_LIMITS.videos).map(referenceVideoToFile));
    videoFiles.forEach((file) => body.append("input_reference[]", file));
    const audioFiles = await Promise.all(audioReferences.slice(0, SEEDANCE_REFERENCE_LIMITS.audios).map(referenceAudioToFile));
    audioFiles.forEach((file) => body.append("input_reference[]", file));
    try {
        const created = unwrapVideoResponse((await axios.post<ApiVideoResponse>(aiApiUrl(config, path), body, { headers: aiHeaders(config), signal: options?.signal })).data);
        const id = readVideoTaskId(created);
        if (!id) throw new Error(apiText("noVideoTaskId"));
        return { id, provider: "openai", model: selectedModel, requestModel: model, path };
    } catch (error) {
        throw new Error(readAxiosError(error, apiText("videoTaskCreateFailed")));
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

async function resolveGrokImageUrl(config: AiConfig, remote: boolean, image: ReferenceImage) {
    const directUrl = image.url || image.dataUrl;
    if (isPublicMediaUrl(directUrl)) return directUrl;
    const dataUrl = await imageToDataUrl(image);
    if (!dataUrl?.startsWith("data:image/")) throw new Error("参考图读取失败，请换一张图片或重新上传");
    if (remote) return uploadReferenceMedia(dataUrlToFile({ ...image, dataUrl }));
    return dataUrl;
}

async function buildGrokImageReferences(config: AiConfig, remote: boolean, references: ReferenceImage[]) {
    return Promise.all(references.slice(0, 4).map((image) => resolveGrokImageUrl(config, remote, image)));
}

async function pollOpenAIVideoTask(config: AiConfig, task: VideoGenerationTask, remote: boolean, options?: RequestOptions): Promise<VideoGenerationTaskState> {
    const path = task.path || "/videos";
    const params = remote && task.requestModel ? { model: task.requestModel } : undefined;
    try {
        const video = unwrapVideoResponse((await axios.get<ApiVideoResponse>(aiApiUrl(config, `${path}/${task.id}`), { headers: aiHeaders(config), params, signal: options?.signal })).data);
        const status = readVideoStatus(video);
        const errorMessage = readVideoErrorMessage(video);
        if (isFailedVideoStatus(status) || (errorMessage && !isCompletedVideoStatus(status))) {
            return { status: "failed", error: errorMessage || (status === "expired" ? "视频生成任务已过期" : "视频生成失败") };
        }
        const videoUrl = readVideoUrl(video);
        if (videoUrl && (!status || isCompletedVideoStatus(status))) {
            refreshGatewayUser(remote);
            return { status: "completed", result: await videoResultFromUrl(videoUrl, config, remote, task, options) };
        }
        if (isCompletedVideoStatus(status)) {
            const content = await axios.get<Blob>(aiApiUrl(config, `${path}/${task.id}/content`), { headers: aiHeaders(config), params, responseType: "blob", signal: options?.signal });
            await assertVideoBlob(content.data);
            refreshGatewayUser(remote);
            return { status: "completed", result: { blob: content.data } };
        }
        return { status: "pending" };
    } catch (error) {
        throw new Error(readAxiosError(error, apiText("videoTaskQueryFailed")));
    }
}

async function createSeedanceTask(config: AiConfig, selectedModel: string, remote: boolean, prompt: string, references: ReferenceImage[], videoReferences: ReferenceVideo[], audioReferences: ReferenceAudio[], options?: RequestOptions): Promise<VideoGenerationTask> {
    if (audioReferences.length && !references.length && !videoReferences.length) {
        throw new Error(apiText("seedanceAudioRequiresVisual"));
    }
    assertSeedanceVideoReferences(videoReferences);
    assertSeedanceAudioReferences(audioReferences);
    const model = config.model;
    const content = await buildSeedanceContent(config, remote, prompt, references, videoReferences, audioReferences);
    if (!content.length) throw new Error(apiText("videoPromptRequired"));
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
        const created = unwrapSeedanceTask((await axios.post<ApiEnvelope<SeedanceTask>>(seedanceApiUrl(config, remote), payload, { headers: aiHeaders(config, "application/json"), signal: options?.signal })).data);
        const id = created.id || readVideoTaskId(created as unknown as VideoResponse);
        if (!id) throw new Error(apiText("seedanceNoTaskId"));
        return { id, provider: "seedance", model: selectedModel, requestModel: model };
    } catch (error) {
        throw new Error(readAxiosError(error, apiText("seedanceTaskCreateFailed")));
    }
}

async function pollSeedanceTask(config: AiConfig, task: VideoGenerationTask, remote: boolean, options?: RequestOptions): Promise<VideoGenerationTaskState> {
    const params = remote && task.requestModel ? { model: task.requestModel } : undefined;
    try {
        const state = unwrapSeedanceTask((await axios.get<ApiEnvelope<SeedanceTask>>(seedanceApiUrl(config, remote, task.id), { headers: aiHeaders(config), params, signal: options?.signal })).data);
        const raw = state as unknown as VideoResponse;
        const status = String(state.status || readVideoStatus(raw)).toLowerCase();
        if (isCompletedVideoStatus(status)) {
            const url = state.content?.video_url || state.content?.url || readVideoUrl(raw);
            if (!url) return { status: "failed", error: apiText("seedanceNoVideoUrl") };
            refreshGatewayUser(remote);
            return { status: "completed", result: await videoResultFromUrl(url, config, remote, task, options) };
        }
        if (isFailedVideoStatus(status))
            return { status: "failed", error: readApiErrorMessage(state.error?.message) || readVideoErrorMessage(raw) || apiText(status === "expired" ? "seedanceVideoTimeout" : "seedanceVideoFailed") };
        return { status: "pending" };
    } catch (error) {
        throw new Error(readAxiosError(error, apiText("seedanceTaskQueryFailed")));
    }
}

function assertSeedanceVideoReferences(videoReferences: ReferenceVideo[]) {
    const error = seedanceVideoReferenceError(videoReferences);
    if (error) throw new Error(error);
    let total = 0;
    for (const video of videoReferences) {
        if (!video.durationMs) continue;
        if (video.durationMs < 2000 || video.durationMs > 15000) throw new Error(apiText("seedanceVideoDuration"));
        total += video.durationMs;
    }
    if (total > 15000) throw new Error(apiText("seedanceVideoTotalDuration"));
}

function assertSeedanceAudioReferences(audioReferences: ReferenceAudio[]) {
    let total = 0;
    for (const audio of audioReferences) {
        if (!audio.durationMs) continue;
        if (audio.durationMs < 2000 || audio.durationMs > 15000) throw new Error(apiText("seedanceAudioDuration"));
        total += audio.durationMs;
    }
    if (total > 15000) throw new Error(apiText("seedanceAudioTotalDuration"));
}

/** 网关暴露的是 OpenAI 风格的 /videos；火山方舟原生接口是 /contents/generations/tasks。 */
function seedanceApiUrl(config: AiConfig, remote: boolean, taskId?: string) {
    if (remote) return buildApiUrl(config.baseUrl, `/videos${taskId ? `/${encodeURIComponent(taskId)}` : ""}`);
    return buildApiUrl(config.baseUrl, `/contents/generations/tasks${taskId ? `/${encodeURIComponent(taskId)}` : ""}`);
}

async function buildSeedanceContent(config: AiConfig, remote: boolean, prompt: string, references: ReferenceImage[], videoReferences: ReferenceVideo[], audioReferences: ReferenceAudio[]) {
    const content: Array<Record<string, unknown>> = [];
    const text = buildSeedancePromptText(prompt, references, videoReferences, audioReferences);
    if (text) content.push({ type: "text", text });
    for (const image of references.slice(0, SEEDANCE_REFERENCE_LIMITS.images)) {
        content.push({ type: "image_url", image_url: { url: await resolveSeedanceImageUrl(config, remote, image) }, role: "reference_image" });
    }
    for (const video of videoReferences.slice(0, SEEDANCE_REFERENCE_LIMITS.videos)) {
        content.push({ type: "video_url", video_url: { url: await resolveSeedanceVideoUrl(remote, video) }, role: "reference_video" });
    }
    for (const audio of audioReferences.slice(0, SEEDANCE_REFERENCE_LIMITS.audios)) {
        content.push({ type: "audio_url", audio_url: { url: await resolveSeedanceAudioUrl(remote, audio) }, role: "reference_audio" });
    }
    return content;
}

async function resolveSeedanceImageUrl(config: AiConfig, remote: boolean, image: ReferenceImage) {
    const directUrl = image.url || image.dataUrl;
    if (isPublicMediaUrl(directUrl) || directUrl.startsWith("asset://")) return directUrl;
    const dataUrl = await imageToDataUrl(image);
    if (!dataUrl) throw new Error(apiText("referenceImageReadFailed"));
    if (remote) return uploadReferenceMedia(dataUrlToFile({ ...image, dataUrl }));
    return dataUrl;
}

async function resolveSeedanceVideoUrl(remote: boolean, video: ReferenceVideo) {
    if (isPublicMediaUrl(video.url) || video.url.startsWith("asset://")) return video.url;
    let blob: Blob | null = null;
    if (video.storageKey) blob = await getMediaBlob(video.storageKey);
    if (!blob && video.url?.startsWith("blob:")) blob = await (await fetch(video.url)).blob();
    if (!blob) throw new Error(apiText("invalidReferenceVideo"));
    const file = new File([blob], video.name || "reference-video.mp4", { type: video.type || blob.type || "video/mp4" });
    return remote ? uploadReferenceMedia(file) : blobToDataUrl(blob);
}

async function resolveSeedanceAudioUrl(remote: boolean, audio: ReferenceAudio) {
    if (isPublicMediaUrl(audio.url) || audio.url.startsWith("asset://")) return audio.url;
    let blob: Blob | null = null;
    if (audio.storageKey) blob = await getMediaBlob(audio.storageKey);
    if (!blob && audio.url?.startsWith("blob:")) blob = await (await fetch(audio.url)).blob();
    if (!blob) throw new Error(apiText("invalidReferenceAudio"));
    const file = new File([blob], audio.name || "reference-audio.mp3", { type: audio.type || blob.type || "audio/mpeg" });
    return remote ? uploadReferenceMedia(file) : blobToDataUrl(blob);
}

/** 本地素材没有公网地址时，先传给后端换一个可被服务商回source的 URL。 */
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

async function videoResultFromUrl(url: string, config: AiConfig, remote: boolean, task?: VideoGenerationTask, options?: RequestOptions): Promise<VideoGenerationResult> {
    const remoteContentUrl = remote ? remoteVideoContentProxyUrl(config, url) : "";
    try {
        if (remoteContentUrl) {
            const response = await axios.get<Blob>(remoteContentUrl, { headers: aiHeaders(config), params: task?.requestModel ? { model: task.requestModel } : undefined, responseType: "blob", signal: options?.signal });
            await assertVideoBlob(response.data);
            return { blob: response.data };
        }
        if (!remote && shouldSendVideoContentAuth(config, url)) {
            // 浏览器跨域取不了带鉴权的视频内容，交给后端代下载。
            const response = await axios.post<Blob>("/api/video-content", { url, apiKey: config.apiKey }, { responseType: "blob", headers: gatewayAuthHeaders(), signal: options?.signal });
            await assertVideoBlob(response.data);
            return { blob: response.data };
        }
        const headers = shouldSendVideoContentAuth(config, url) ? aiHeaders(config) : undefined;
        const response = await axios.get<Blob>(url, { responseType: "blob", ...(headers ? { headers } : {}), signal: options?.signal });
        await assertVideoBlob(response.data);
        return { blob: response.data };
    } catch (error) {
        if (axios.isCancel(error) || options?.signal?.aborted) throw error;
        if (remoteContentUrl) throw new Error(readAxiosError(error, "视频已生成，但下载视频内容失败"));
        return { url, mimeType: "video/mp4" };
    }
}

function gatewayAuthHeaders() {
    const token = useUserStore.getState().token;
    return token ? { Authorization: `Bearer ${token}` } : undefined;
}

function remoteVideoContentProxyUrl(config: AiConfig, url: string) {
    const taskId = videoContentTaskId(url);
    return taskId ? aiApiUrl(config, `/videos/${encodeURIComponent(taskId)}/content`) : "";
}

function videoContentTaskId(value: string) {
    const path = videoContentPath(value);
    const id = path.match(/\/(?:v1\/)?videos\/([^/]+)\/content\/?$/i)?.[1] || "";
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
    return url.startsWith(videosBaseUrl) && /\/content(?:\?|$)/.test(url);
}

function assertVideoConfig(config: AiConfig, model: string) {
    if (!model) throw new Error(apiText("videoModelRequired"));
    if (!config.baseUrl.trim()) throw new Error(apiText("baseUrlRequired"));
    if (!config.apiKey.trim()) throw new Error(apiText("apiKeyRequired"));
    if (config.apiFormat === "gemini") throw new Error(apiText("geminiVideoUnsupported"));
}

function normalizeVideoSeconds(value: string) {
    const seconds = Math.floor(Number(value) || 6);
    return String(Math.max(1, Math.min(20, seconds)));
}

function normalizeVideoSize(value: string) {
    if (value === "auto") return null;
    const size = value || "1280x720";
    if (/^\d+x\d+$/.test(size)) return size;
    return ["9:16", "2:3", "3:4"].includes(size) ? "720x1280" : "1280x720";
}

function normalizeVideoResolution(value: string) {
    const normalized = String(value || "")
        .trim()
        .toLowerCase();
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
    const labels = references
        .slice(0, 7)
        .map((_, index) => `<IMAGE_${index + 1}>`)
        .join("、");
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
    return unwrapEnvelope(payload, apiText("noVideoTask"));
}

function unwrapSeedanceTask(payload: ApiEnvelope<SeedanceTask>) {
    return unwrapEnvelope(payload, apiText("seedanceNoTask"));
}

function isCompletedVideoStatus(status: string) {
    return status === "done" || status === "completed" || status === "complete" || status === "succeeded" || status === "success";
}

function isFailedVideoStatus(status: string) {
    return status === "failed" || status === "failure" || status === "error" || status === "expired" || status === "cancelled" || status === "canceled";
}

function unwrapEnvelope<T>(payload: ApiEnvelope<T>, emptyMessage: string): T {
    if (!payload) throw new Error(emptyMessage);
    if (typeof payload === "object" && "code" in payload && payload.code !== undefined) {
        if (payload.code !== 0 && payload.code !== "0") throw new Error(readApiErrorMessage(payload) || apiText("requestFailed"));
        if (!payload.data) throw new Error(emptyMessage);
        return payload.data;
    }
    return payload as T;
}

function readApiErrorMessage(value: unknown): string {
    if (!value) return "";
    if (typeof value === "string") {
        try {
            const parsed = JSON.parse(value);
            const inner = readApiErrorMessage(parsed) || value;
            if (inner === value && typeof parsed === "object" && Object.keys(parsed).length === 0) return "";
            return inner;
        } catch {
            if (/<[a-z][\s\S]*>/i.test(value)) return apiText("htmlError", { preview: `${value.slice(0, 80)}...` });
            return value;
        }
    }
    if (typeof value !== "object") return "";
    const payload = value as { msg?: unknown; message?: unknown; error?: unknown; detail?: unknown };
    // error may be a string or an object containing a message.
    const errorMsg =
        typeof payload.error === "string"
            ? payload.error
            : (payload.error as { message?: unknown })?.message;
    return (
        readApiErrorMessage(payload.msg) ||
        readApiErrorMessage(payload.message) ||
        readApiErrorMessage(errorMsg) ||
        readApiErrorMessage(payload.detail) ||
        ""
    );
}

function readAxiosError(error: unknown, fallback: string) {
    if (axios.isCancel(error)) return apiText("requestCanceled");
    if (axios.isAxiosError<{ error?: { message?: string }; msg?: string; message?: string; code?: number | string }>(error)) {
        const responseData = error.response?.data;
        return normalizeVideoErrorMessage(errorResponseMessage(responseData) || statusMessage(error.response?.status, fallback));
    }
    if (error instanceof DOMException && error.name === "AbortError") return apiText("requestCanceled");
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
    if (status === 401 || status === 403) return apiText("authenticationFailed");
    if (status === 429) return apiText("rateLimited");
    return status ? `${fallback}（${status}）` : fallback;
}

async function assertVideoBlob(blob: Blob) {
    if (!blob.type.includes("json")) return;
    let payload: { code?: number; msg?: string; error?: string | { message?: string } };
    try {
        payload = JSON.parse(await blob.text()) as { code?: number; msg?: string; error?: string | { message?: string } };
    } catch {
        return;
    }
    if (typeof payload.code === "number" && payload.code !== 0) throw new Error(readApiErrorMessage(payload) || apiText("videoDownloadFailed"));
    if (typeof payload.error === "string" && payload.error) throw new Error(readApiErrorMessage(payload.error) || payload.error);
    if (payload.error && typeof payload.error === "object" && payload.error.message) throw new Error(readApiErrorMessage(payload.error.message) || payload.error.message);
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

function isXAIBaseUrl(baseUrl: string) {
    try {
        return new URL(baseUrl).hostname.toLowerCase().endsWith("x.ai");
    } catch {
        return /(^|\.)x\.ai(?:\/|$)/i.test(baseUrl);
    }
}

function delay(ms: number, signal?: AbortSignal) {
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

function blobToDataUrl(blob: Blob) {
    return new Promise<string>((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(String(reader.result || ""));
        reader.onerror = () => reject(new Error(apiText("localAssetReadFailed")));
        reader.readAsDataURL(blob);
    });
}
