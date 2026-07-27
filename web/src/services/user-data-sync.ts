import { hydrateAsset, imageLogStore, mergeById, readStoredLogs, replaceStoredLogs, videoLogStore, waitForHydration, type StoredLog } from "@/services/app-sync";
import { fetchUserDataSnapshot, saveUserDataSnapshot, type UserDataDomain } from "@/services/api/user-data";
import type { Asset } from "@/stores/use-asset-store";
import { useAssetStore } from "@/stores/use-asset-store";
import type { CanvasProject } from "@/stores/canvas/use-canvas-store";
import { useCanvasStore } from "@/stores/canvas/use-canvas-store";

type CanvasData = { projects: CanvasProject[] };
type AssetData = { assets: Asset[] };
type LogData = { logs: StoredLog[] };

const LAST_USER_KEY = "infinite-canvas:last-synced-user";
const SAVE_DELAY_MS = 900;

const saveTimers = new Map<UserDataDomain, ReturnType<typeof setTimeout>>();
const savedPayloads = new Map<UserDataDomain, string>();
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
    unsubscribers.forEach((unsubscribe) => unsubscribe());
    unsubscribers = [];
}

function setupSubscriptions() {
    if (unsubscribers.length) return;
    // ponytail: 工作台日志直接写 localforage，没有可订阅的 store，
    // 因此只在页面隐藏时整体回存；要做到实时回存需改 image/video 页面派发变更事件。
    const onHidden = () => {
        if (document.visibilityState === "hidden") void saveLogDomains();
    };
    document.addEventListener("visibilitychange", onHidden);
    unsubscribers = [
        useCanvasStore.subscribe(() => queueSave("canvas")),
        useAssetStore.subscribe(() => queueSave("assets")),
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
    const data = await domainData(domain);
    // 日志域按页面隐藏整体回存，没有变更事件可依赖，因此比对上次快照跳过重复上传。
    const payload = JSON.stringify(data);
    if (savedPayloads.get(domain) === payload) return;
    await saveUserDataSnapshot(domain, data, token)
        .then(() => savedPayloads.set(domain, payload))
        .catch(() => undefined);
}

async function domainData(domain: UserDataDomain): Promise<CanvasData | AssetData | LogData> {
    if (domain === "canvas") return { projects: useCanvasStore.getState().projects };
    if (domain === "assets") return { assets: useAssetStore.getState().assets };
    return { logs: await readStoredLogs(domain === "image-workbench" ? imageLogStore : videoLogStore) };
}
