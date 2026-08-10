import { hydrateAsset, imageLogStore, mergeById, readStoredLogs, replaceStoredLogs, videoLogStore, waitForHydration, type StoredLog } from "@/services/app-sync";
import { fetchUserDataSnapshot, saveCanvasProjects, saveUserDataSnapshot, type UserDataDomain } from "@/services/api/user-data";
import type { Asset } from "@/stores/use-asset-store";
import { useAssetStore } from "@/stores/use-asset-store";
import type { CanvasProject } from "@/stores/canvas/use-canvas-store";
import { useCanvasStore } from "@/stores/canvas/use-canvas-store";

type CanvasData = { projects: CanvasProject[] };
type AssetData = { assets: Asset[] };
type LogData = { logs: StoredLog[] };

type SyncErrorListener = (message: string) => void;

const LAST_USER_KEY = "infinite-canvas:last-synced-user";
const SAVE_DELAY_MS = 5000;

const saveTimers = new Map<UserDataDomain, ReturnType<typeof setTimeout>>();
const savedPayloads = new Map<UserDataDomain, string>();
// 画布按 project 逐个比对，只上传变化的那些；值是上次成功上传的序列化结果。
const savedProjects = new Map<string, string>();
const errorListeners = new Set<SyncErrorListener>();
let savedProjectIds: string[] = [];
let currentToken = "";
let syncedUserId = "";
let unsubscribers: Array<() => void> = [];
let suppressSave = false;

export async function startUserDataSync(token: string, userId: string) {
    if (!token || !userId) return;
    currentToken = token;
    setupSubscriptions();
    if (syncedUserId === userId) return;
    syncedUserId = userId;
    suppressSave = true;
    try {
        // 同一浏览器换账号时先清空本地库，避免上一个账号的画布/资产混入。
        if (window.localStorage.getItem(LAST_USER_KEY) !== userId) {
            await clearLocalData();
            window.localStorage.setItem(LAST_USER_KEY, userId);
        }
        await Promise.all([waitForHydration(useCanvasStore), waitForHydration(useAssetStore)]);
        await Promise.all([hydrateCanvas(token), hydrateAssets(token), hydrateLogs("image-workbench", token), hydrateLogs("video-workbench", token)]);
    } finally {
        suppressSave = false;
    }
    queueSave("canvas");
    queueSave("assets");
    void saveLogDomains();
}

export function stopUserDataSync() {
    currentToken = "";
    syncedUserId = "";
    for (const timer of saveTimers.values()) clearTimeout(timer);
    saveTimers.clear();
    savedPayloads.clear();
    savedProjects.clear();
    savedProjectIds = [];
    unsubscribers.forEach((unsubscribe) => unsubscribe());
    unsubscribers = [];
}

/** 同步失败只能在这里上报：它跑在定时器里，抛出去只会变成 unhandled rejection。 */
export function onUserDataSyncError(listener: SyncErrorListener) {
    errorListeners.add(listener);
    return () => {
        errorListeners.delete(listener);
    };
}

/** 立刻写出所有排队中的变更，用于页面切到后台时保底。 */
export function flushUserDataSync() {
    for (const [domain, timer] of [...saveTimers.entries()]) {
        clearTimeout(timer);
        saveTimers.delete(domain);
        void saveDomain(domain, currentToken);
    }
}

function setupSubscriptions() {
    if (unsubscribers.length) return;
    // ponytail: 工作台日志直接写 localforage，没有可订阅的 store，
    // 因此只在页面隐藏时整体回存；要做到实时回存需改 image/video 页面派发变更事件。
    const onHidden = () => {
        if (document.visibilityState !== "hidden") return;
        flushUserDataSync();
        void saveLogDomains();
    };
    document.addEventListener("visibilitychange", onHidden);
    unsubscribers = [
        // 只认 projects/assets 引用变化，hydrated 之类的状态位不再触发上传。
        useCanvasStore.subscribe((state, previous) => {
            if (state.projects !== previous.projects) queueSave("canvas");
        }),
        useAssetStore.subscribe((state, previous) => {
            if (state.assets !== previous.assets) queueSave("assets");
        }),
        () => document.removeEventListener("visibilitychange", onHidden),
    ];
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
        }, SAVE_DELAY_MS),
    );
}

async function clearLocalData() {
    useCanvasStore.getState().replaceProjects([]);
    useAssetStore.getState().replaceAssets([]);
    await Promise.all([imageLogStore.clear(), videoLogStore.clear()]);
}

async function hydrateCanvas(token: string) {
    const remote = await fetchUserDataSnapshot<CanvasData>("canvas", token).catch(() => null);
    const projects = remote?.data?.projects;
    if (!Array.isArray(projects) || !projects.length) return;
    // 记下服务端现有内容，首次回存才只推本地真正多出来的部分，而不是整库重传。
    savedProjects.clear();
    for (const project of projects) savedProjects.set(project.id, JSON.stringify(project));
    savedProjectIds = projects.map((project) => project.id);
    useCanvasStore.getState().replaceProjects(mergeById(useCanvasStore.getState().projects, projects, "updatedAt"));
}

async function hydrateAssets(token: string) {
    const remote = await fetchUserDataSnapshot<AssetData>("assets", token).catch(() => null);
    const assets = remote?.data?.assets;
    if (!Array.isArray(assets) || !assets.length) return;
    const merged = mergeById(useAssetStore.getState().assets, assets, "updatedAt");
    useAssetStore.getState().replaceAssets(await Promise.all(merged.map(hydrateAsset)));
}

async function hydrateLogs(domain: "image-workbench" | "video-workbench", token: string) {
    const remote = await fetchUserDataSnapshot<LogData>(domain, token).catch(() => null);
    const logs = remote?.data?.logs;
    if (!Array.isArray(logs) || !logs.length) return;
    const store = domain === "image-workbench" ? imageLogStore : videoLogStore;
    await replaceStoredLogs(store, mergeById(await readStoredLogs(store), logs, "createdAt"));
}

async function saveLogDomains() {
    if (!currentToken || suppressSave) return;
    await Promise.all([saveDomain("image-workbench", currentToken), saveDomain("video-workbench", currentToken)]);
}

async function saveDomain(domain: UserDataDomain, token: string) {
    if (!token) return;
    try {
        if (domain === "canvas") await saveCanvasDomain(token);
        else await saveSnapshotDomain(domain, token);
    } catch (error) {
        reportSyncError(error);
    }
}

/**
 * 算出这次要上传哪些画布：只有序列化结果和上次成功上传的不一致才算变更；
 * 画布列表本身没变且没有内容变更时 skip，避免空跑一次请求。
 */
export function planCanvasSave(projects: CanvasProject[], saved: Map<string, string>, savedIds: string[]) {
    const ids = projects.map((project) => project.id);
    const changed = projects.map((project) => ({ project, json: JSON.stringify(project) })).filter((entry) => saved.get(entry.project.id) !== entry.json);
    return { ids, changed, skip: !changed.length && sameIds(ids, savedIds) };
}

async function saveCanvasDomain(token: string) {
    const { ids, changed, skip } = planCanvasSave(useCanvasStore.getState().projects, savedProjects, savedProjectIds);
    if (skip) return;
    await saveCanvasProjects(
        changed.map((entry) => entry.project),
        ids,
        token,
    );
    for (const entry of changed) savedProjects.set(entry.project.id, entry.json);
    const kept = new Set(ids);
    for (const id of [...savedProjects.keys()]) if (!kept.has(id)) savedProjects.delete(id);
    savedProjectIds = ids;
}

async function saveSnapshotDomain(domain: UserDataDomain, token: string) {
    const data = await domainData(domain);
    // 日志域按页面隐藏整体回存，没有变更事件可依赖，因此比对上次快照跳过重复上传。
    const payload = JSON.stringify(data);
    if (savedPayloads.get(domain) === payload) return;
    await saveUserDataSnapshot(domain, data, token);
    savedPayloads.set(domain, payload);
}

async function domainData(domain: UserDataDomain): Promise<AssetData | LogData> {
    if (domain === "assets") return { assets: useAssetStore.getState().assets };
    return { logs: await readStoredLogs(domain === "image-workbench" ? imageLogStore : videoLogStore) };
}

function sameIds(left: string[], right: string[]) {
    return left.length === right.length && left.every((id, index) => id === right[index]);
}

function reportSyncError(error: unknown) {
    const message = error instanceof Error ? error.message : String(error);
    console.error("[user-data-sync] save failed:", error);
    errorListeners.forEach((listener) => listener(message));
}
