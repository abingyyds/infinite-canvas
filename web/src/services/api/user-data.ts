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
