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

/** 只上传改动过的画布，keepIds 是当前完整的画布 id 列表，服务端据此排序并删除多余的。 */
export async function saveCanvasProjects<T>(projects: T[], keepIds: string[], token: string) {
    return apiPost<UserDataSnapshot<null>>("/api/canvas/projects", { projects, keepIds }, token);
}
