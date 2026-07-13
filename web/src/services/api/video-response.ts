export type VideoResponse = {
    id?: string;
    request_id?: string;
    requestId?: string;
    task_id?: string;
    taskId?: string;
    video_id?: string;
    videoId?: string;
    status?: string;
    state?: string;
    data?: VideoResponse | null;
    progress?: number;
    fail_reason?: string;
    message?: string;
    msg?: string;
    detail?: string;
    error?: string | { code?: string; message?: string } | null;
    video?: { url?: string; video_url?: string; duration?: number } | null;
    url?: string;
    videoUrl?: string;
    video_url?: string;
    download_url?: string;
    file_url?: string;
    video_uri?: string;
    result_url?: string;
    local_preview_url?: string;
    content?: string | { url?: string; video_url?: string; result_url?: string; local_preview_url?: string; message?: string; fail_reason?: string; error?: string | { code?: string; message?: string } | null } | null;
    output?: { url?: string; video_url?: string; videos?: Array<{ url?: string; video_url?: string }> } | null;
    result?: unknown;
};

export function readVideoTaskId(payload: VideoResponse) {
    for (const entry of iteratePayloads(payload)) {
        const taskId = pickText([entry.request_id, entry.requestId, entry.task_id, entry.taskId, entry.video_id, entry.videoId, entry.id]);
        if (taskId) return taskId;
    }
    return undefined;
}

export function readVideoStatus(payload: VideoResponse) {
    for (const entry of iteratePayloads(payload)) {
        const status = normalizeText(entry.status || entry.state).toLowerCase();
        if (status) return status;
    }
    return "";
}

export function readVideoUrl(payload: VideoResponse) {
    return readStructuredVideoUrl(payload);
}

export function readVideoErrorMessage(payload: VideoResponse) {
    return readStructuredErrorText(payload);
}

function* iteratePayloads(payload: VideoResponse | null | undefined): Generator<VideoResponse> {
    const seen = new Set<VideoResponse>();
    let current = payload ?? undefined;
    while (current && typeof current === "object" && !seen.has(current)) {
        seen.add(current);
        yield current;
        current = isRecord(current.data) ? (current.data as VideoResponse) : undefined;
    }
}

function readStructuredVideoUrl(value: unknown, depth = 0): string {
    if (depth > 5 || value == null) return "";
    if (typeof value === "string") {
        const text = normalizeText(value);
        if (!text) return "";
        if (isVideoLocation(text)) return text;
        const parsed = parseStructuredValue(text);
        return parsed ? readStructuredVideoUrl(parsed, depth + 1) : "";
    }
    if (Array.isArray(value)) {
        return pickFirst(value.map((entry) => readStructuredVideoUrl(entry, depth + 1)));
    }
    if (!isRecord(value)) return "";

    const video = asRecord(value.video);
    const output = asRecord(value.output);
    return pickFirst([
        readStructuredVideoUrl(video?.url, depth + 1),
        readStructuredVideoUrl(video?.video_url, depth + 1),
        readStructuredVideoUrl(value.videoUrl, depth + 1),
        readStructuredVideoUrl(value.video_url, depth + 1),
        readStructuredVideoUrl(value.download_url, depth + 1),
        readStructuredVideoUrl(value.file_url, depth + 1),
        readStructuredVideoUrl(value.video_uri, depth + 1),
        readStructuredVideoUrl(value.result_url, depth + 1),
        readStructuredVideoUrl(value.local_preview_url, depth + 1),
        readStructuredVideoUrl(value.url, depth + 1),
        readStructuredVideoUrl(value.content, depth + 1),
        readStructuredVideoUrl(output?.video_url, depth + 1),
        readStructuredVideoUrl(output?.url, depth + 1),
        readStructuredVideoUrl(output?.videos, depth + 1),
        readStructuredVideoUrl(value.result, depth + 1),
        readStructuredVideoUrl(value.data, depth + 1),
    ]);
}

function readStructuredErrorText(value: unknown, depth = 0, trusted = false): string {
    if (depth > 5 || value == null) return "";
    if (typeof value === "string") {
        const text = normalizeText(value);
        if (!text) return "";
        const parsed = parseStructuredValue(text);
        if (parsed) return readStructuredErrorText(parsed, depth + 1, trusted);
        if (isVideoLocation(text) || isIgnorableTaskMessage(text)) return "";
        return trusted || looksLikeErrorText(text) ? text : "";
    }
    if (Array.isArray(value)) {
        return pickFirst(value.map((entry) => readStructuredErrorText(entry, depth + 1, trusted)));
    }
    if (!isRecord(value)) return "";

    const error = asRecord(value.error);
    return pickFirst([
        readStructuredErrorText(value.error, depth + 1, true),
        readStructuredErrorText(error?.message, depth + 1, true),
        readStructuredErrorText(error?.code, depth + 1, true),
        readStructuredErrorText(value.fail_reason, depth + 1, true),
        readStructuredErrorText(value.message, depth + 1),
        readStructuredErrorText(value.msg, depth + 1),
        readStructuredErrorText(value.detail, depth + 1, true),
        readStructuredErrorText(value.content, depth + 1),
        readStructuredErrorText(value.result_url, depth + 1),
        readStructuredErrorText(value.local_preview_url, depth + 1),
        readStructuredErrorText(value.url, depth + 1),
        readStructuredErrorText(value.video_url, depth + 1),
        readStructuredErrorText(value.videoUrl, depth + 1),
        readStructuredErrorText(value.result, depth + 1),
        readStructuredErrorText(value.output, depth + 1),
        readStructuredErrorText(value.data, depth + 1),
    ]);
}

function parseStructuredValue(value: string) {
    if (!/^[\[{]/.test(value)) return null;
    try {
        return JSON.parse(value) as unknown;
    } catch {
        return null;
    }
}

function looksLikeErrorText(value: string) {
    return /fail|error|invalid|denied|reject|forbidden|unauthori|quota|limit|timeout|timed out|expired|unsupported|missing|insufficient|exceed|violation|policy|unable|cannot|can't|could not|bad request|internal|unavailable|wrong|cancell?ed|abort|拒绝|失败|错误|超时|过期|无效|限制|不足|不支持|缺少|异常|违规/i.test(value);
}

function isIgnorableTaskMessage(value: string) {
    return /^(queued|queueing|pending|processing|running|starting|submitted|accepted|created|scheduled|generating|rendering|working|in[\s-]?progress|success|succeeded|completed|complete|done)(?:[\s.:!-]|$)|^(排队中|处理中|生成中|运行中|等待中|已提交|已创建|已完成|处理中，请稍后)/i.test(value);
}

function isVideoLocation(value: string) {
    return /^(https?:\/\/|\/\/|\/|blob:|asset:\/\/|data:video\/)/i.test(value);
}

function pickFirst(values: string[]) {
    return values.find(Boolean) || "";
}

function pickText(values: Array<string | undefined>) {
    return values.map((value) => normalizeText(value)).find(Boolean) || "";
}

function normalizeText(value: unknown) {
    return typeof value === "string" ? value.trim() : "";
}

function isRecord(value: unknown): value is Record<string, unknown> {
    return Boolean(value) && typeof value === "object";
}

function asRecord(value: unknown) {
    return isRecord(value) ? value : undefined;
}
