"use client";

import localforage from "localforage";

import type { CanvasProject } from "@/app/(user)/canvas/stores/use-canvas-store";
import { useCanvasStore } from "@/app/(user)/canvas/stores/use-canvas-store";
import { localForageStorage } from "@/lib/localforage-storage";
import { scopedStoreKey } from "@/lib/user-scope";
import { readScopedStoredLogs, replaceScopedStoredLogs } from "@/services/app-sync";
import { fetchUserDataSnapshot, saveUserDataSnapshot, type UserDataDomain } from "@/services/api/user-data";
import type { Asset } from "@/stores/use-asset-store";
import { useAssetStore } from "@/stores/use-asset-store";

type CanvasData = { projects: CanvasProject[] };
type AssetData = { assets: Asset[] };
type LogData = { logs: Array<Record<string, unknown> & { id?: string }> };
type DomainData = CanvasData | AssetData | LogData;

const IMAGE_LOG_STORE_KEY = "infinite-canvas:image_generation_logs";
const VIDEO_LOG_STORE_KEY = "infinite-canvas:video_generation_logs";
const CANVAS_STORE_KEY = "infinite-canvas:canvas_store";
const ASSET_STORE_KEY = "infinite-canvas:asset_store";
const imageLogStore = localforage.createInstance({ name: "infinite-canvas", storeName: "image_generation_logs" });
const videoLogStore = localforage.createInstance({ name: "infinite-canvas", storeName: "video_generation_logs" });

const saveTimers = new Map<UserDataDomain, ReturnType<typeof setTimeout>>();
let hydratedToken = "";
let currentToken = "";
let unsubscribers: Array<() => void> = [];
let suppressSave = false;

export async function startUserDataSync(token: string) {
    if (typeof window === "undefined" || !token) return;
    currentToken = token;
    setupSubscriptions();
    if (hydratedToken === token) return;
    hydratedToken = token;
    suppressSave = true;
    try {
        await hydrateScopedLocalStores();
        await Promise.all([hydrateCanvas(token), hydrateAssets(token), hydrateLogs("image-workbench", token), hydrateLogs("video-workbench", token)]);
    } finally {
        suppressSave = false;
    }
    queueSave("canvas");
    queueSave("assets");
    queueSave("image-workbench");
    queueSave("video-workbench");
}

export function stopUserDataSync() {
    currentToken = "";
    hydratedToken = "";
    for (const timer of saveTimers.values()) clearTimeout(timer);
    saveTimers.clear();
}

export function notifyUserDataChanged(domain: UserDataDomain) {
    queueSave(domain);
}

function setupSubscriptions() {
    if (unsubscribers.length) return;
    unsubscribers = [
        useCanvasStore.subscribe(() => queueSave("canvas")),
        useAssetStore.subscribe(() => queueSave("assets")),
        () => window.removeEventListener("infinite-canvas:image-logs-changed", imageLogsChanged),
        () => window.removeEventListener("infinite-canvas:video-logs-changed", videoLogsChanged),
    ];
    window.addEventListener("infinite-canvas:image-logs-changed", imageLogsChanged);
    window.addEventListener("infinite-canvas:video-logs-changed", videoLogsChanged);
}

function imageLogsChanged() {
    queueSave("image-workbench");
}

function videoLogsChanged() {
    queueSave("video-workbench");
}

function queueSave(domain: UserDataDomain) {
    if (!currentToken || suppressSave) return;
    const existing = saveTimers.get(domain);
    if (existing) clearTimeout(existing);
    saveTimers.set(
        domain,
        setTimeout(() => {
            saveTimers.delete(domain);
            void saveDomain(domain, currentToken);
        }, 900),
    );
}

async function hydrateCanvas(token: string) {
    const remote = await fetchUserDataSnapshot<CanvasData>("canvas", token).catch(() => null);
    const remoteProjects = Array.isArray(remote?.data?.projects) ? remote.data.projects : [];
    if (!remoteProjects.length) return;
    const localProjects = useCanvasStore.getState().projects;
    useCanvasStore.getState().replaceProjects(mergeById(localProjects, remoteProjects, "updatedAt"));
}

async function hydrateScopedLocalStores() {
    await Promise.all([hydrateScopedCanvasStore(), hydrateScopedAssetStore()]);
}

async function hydrateScopedCanvasStore() {
    const stored = await localForageStorage.getItem(scopedStoreKey(CANVAS_STORE_KEY));
    if (stored) {
        await useCanvasStore.persist.rehydrate();
        return;
    }
    useCanvasStore.getState().replaceProjects([]);
}

async function hydrateScopedAssetStore() {
    const stored = await localForageStorage.getItem(scopedStoreKey(ASSET_STORE_KEY));
    if (stored) {
        await useAssetStore.persist.rehydrate();
        return;
    }
    useAssetStore.getState().replaceAssets([]);
}

async function hydrateAssets(token: string) {
    const remote = await fetchUserDataSnapshot<AssetData>("assets", token).catch(() => null);
    const remoteAssets = Array.isArray(remote?.data?.assets) ? remote.data.assets : [];
    if (!remoteAssets.length) return;
    const localAssets = useAssetStore.getState().assets;
    useAssetStore.getState().replaceAssets(mergeById(localAssets, remoteAssets, "updatedAt"));
}

async function hydrateLogs(domain: "image-workbench" | "video-workbench", token: string) {
    const remote = await fetchUserDataSnapshot<LogData>(domain, token).catch(() => null);
    const remoteLogs = Array.isArray(remote?.data?.logs) ? remote.data.logs : [];
    if (!remoteLogs.length) return;
    const store = domain === "image-workbench" ? imageLogStore : videoLogStore;
    const key = domain === "image-workbench" ? IMAGE_LOG_STORE_KEY : VIDEO_LOG_STORE_KEY;
    const localLogs = await readScopedStoredLogs(store, key);
    await replaceScopedStoredLogs(store, key, mergeById(localLogs, remoteLogs, "createdAt"));
    window.dispatchEvent(new Event(domain === "image-workbench" ? "infinite-canvas:image-logs-hydrated" : "infinite-canvas:video-logs-hydrated"));
}

async function saveDomain(domain: UserDataDomain, token: string) {
    const data = await domainData(domain);
    await saveUserDataSnapshot(domain, data, token).catch(() => undefined);
}

async function domainData(domain: UserDataDomain): Promise<DomainData> {
    if (domain === "canvas") return { projects: useCanvasStore.getState().projects };
    if (domain === "assets") return { assets: useAssetStore.getState().assets };
    if (domain === "image-workbench") return { logs: await readScopedStoredLogs(imageLogStore, IMAGE_LOG_STORE_KEY) };
    return { logs: await readScopedStoredLogs(videoLogStore, VIDEO_LOG_STORE_KEY) };
}

function mergeById<T extends { id?: string }>(local: T[], remote: T[], timeKey: string) {
    const items = new Map<string, T>();
    remote.forEach((item) => {
        if (item.id) items.set(item.id, item);
    });
    local.forEach((item) => {
        if (!item.id) return;
        const current = items.get(item.id);
        if (!current || itemTime(item as Record<string, unknown>, timeKey) >= itemTime(current as Record<string, unknown>, timeKey)) items.set(item.id, item);
    });
    return Array.from(items.values()).sort((a, b) => itemTime(b as Record<string, unknown>, timeKey) - itemTime(a as Record<string, unknown>, timeKey));
}

function itemTime(item: Record<string, unknown>, key: string) {
    const value = item[key];
    if (typeof value === "number") return value;
    if (typeof value === "string") return Date.parse(value) || 0;
    return 0;
}

function waitForHydration<T extends { hydrated: boolean }>(store: { getState: () => T; subscribe: (listener: (state: T) => void) => () => void }) {
    if (store.getState().hydrated) return Promise.resolve();
    return new Promise<void>((resolve) => {
        const unsubscribe = store.subscribe((state) => {
            if (!state.hydrated) return;
            unsubscribe();
            resolve();
        });
    });
}
