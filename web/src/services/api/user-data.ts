import { apiGet, apiPost } from "@/services/api/request";

export type UserDataDomain = "canvas" | "assets" | "image-workbench" | "video-workbench";

export type UserDataSnapshot<T> = {
    domain: UserDataDomain;
    data: T | null;
    updatedAt: string;
};

export async function fetchUserDataSnapshot<T>(domain: UserDataDomain, token: string) {
    return apiGet<UserDataSnapshot<T>>(`/api/user-data/${encodeURIComponent(domain)}`, undefined, token);
}

export async function saveUserDataSnapshot<T>(domain: UserDataDomain, data: T, token: string) {
    return apiPost<UserDataSnapshot<T>>(`/api/user-data/${encodeURIComponent(domain)}`, { data }, token);
}

export type CanvasSaveResult = {
    updatedAt: string;
    revisions?: Record<string, number>;
    conflicts?: Array<{ id: string; revision: number; data: unknown }>;
};

/**
 * 只上传改动过的画布。keepIds 是当前完整的画布 id 列表，服务端据此排序；删除走显式的
 * deleteIds；baseRevisions 是每个上传画布所基于的版本号，对不上服务端会返回 409 和它那一份。
 */
export async function saveCanvasProjects<T>(projects: T[], keepIds: string[], deleteIds: string[], baseRevisions: Record<string, number>, token: string) {
    return apiPost<CanvasSaveResult>("/api/canvas/projects", { projects, keepIds, deleteIds, baseRevisions }, token);
}
