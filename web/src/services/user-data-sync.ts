import { hydrateAsset, imageLogStore, mergeById, readStoredLogs, replaceStoredLogs, videoLogStore, waitForHydration, type StoredLog } from "@/services/app-sync";
import { fetchUserDataSnapshot, saveCanvasProjects, saveUserDataSnapshot, type CanvasSaveResult, type UserDataDomain } from "@/services/api/user-data";
import type { Asset } from "@/stores/use-asset-store";
import { useAssetStore } from "@/stores/use-asset-store";
import type { CanvasProject } from "@/stores/canvas/use-canvas-store";
import { useCanvasStore } from "@/stores/canvas/use-canvas-store";

type CanvasData = { projects: CanvasProject[]; revisions?: Record<string, number> };
type AssetData = { assets: Asset[] };
type LogData = { logs: StoredLog[] };

type SyncErrorListener = (message: string) => void;

const LAST_USER_KEY = "infinite-canvas:last-synced-user";
const SAVE_DELAY_MS = 5000;

const saveTimers = new Map<UserDataDomain, ReturnType<typeof setTimeout>>();
// 同一 domain 的保存排成一条链：并发上传时旧快照可能后到，把刚存好的新内容覆盖掉。
const saveChains = new Map<UserDataDomain, Promise<void>>();
const savedPayloads = new Map<UserDataDomain, string>();
// 画布按 project 逐个比对，只上传变化的那些；值是上次成功上传的序列化结果。
const savedProjects = new Map<string, string>();
// 每个画布在服务端的版本号，保存时回传给服务端做乐观锁。
const savedRevisions = new Map<string, number>();
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
    saveChains.clear();
    savedPayloads.clear();
    savedProjects.clear();
    savedRevisions.clear();
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
    savedRevisions.clear();
    for (const project of projects) savedProjects.set(project.id, JSON.stringify(project));
    for (const [id, revision] of Object.entries(remote?.data?.revisions || {})) savedRevisions.set(id, revision);
    savedProjectIds = projects.map((project) => project.id);
    // 还没同步出去的删除意图要留着，否则刚删掉的画布会被服务端那份重新拉回来。
    const state = useCanvasStore.getState();
    const deleted = new Set(state.deletedProjects.map((item) => item.id));
    state.replaceProjects(
        mergeById(
            state.projects,
            projects.filter((project) => !deleted.has(project.id)),
            "updatedAt",
        ),
        state.deletedProjects,
    );
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

/** 把同一 key 的任务排成队列，保证发出顺序就是到达顺序；前一个失败也不掐断后面的。 */
export function chainByKey<K>(chains: Map<K, Promise<void>>, key: K, task: () => Promise<void>) {
    const next = (chains.get(key) ?? Promise.resolve()).then(task, task);
    chains.set(key, next);
    return next;
}

function saveDomain(domain: UserDataDomain, token: string) {
    if (!token) return Promise.resolve();
    return chainByKey(saveChains, domain, async () => {
        try {
            if (domain === "canvas") await saveCanvasDomain(token);
            else await saveSnapshotDomain(domain, token);
        } catch (error) {
            reportSyncError(error);
        }
    });
}

type Identified = { id: string };

/**
 * 按 id 做三方合并：base 是客户端上次成功上传的那份，因此"一边没有"能分清是新增还是删除。
 * 两边都改了同一条时取本地，用户眼前看到的那份不该被远端悄悄换掉。
 */
function mergeListById<T extends Identified>(base: T[] | null, local: T[], remote: T[]) {
    const remoteMap = new Map(remote.map((item) => [item.id, item]));
    const baseIds = base ? new Set(base.map((item) => item.id)) : null;
    const merged: T[] = [];
    const taken = new Set<string>();
    for (const item of local) {
        // base 有、remote 没有 = 远端删掉了它；没有 base 时无从判断，一律保留
        if (baseIds?.has(item.id) && !remoteMap.has(item.id)) continue;
        merged.push(item);
        taken.add(item.id);
    }
    for (const item of remote) {
        if (taken.has(item.id)) continue;
        // base 有、local 没有 = 本地删掉了它，不要复活
        if (baseIds?.has(item.id)) continue;
        merged.push(item);
    }
    return merged;
}

/** 冲突时用三方合并代替"整个画布二选一"，后者会把输的那边的全部节点丢掉。 */
export function mergeCanvasProject(base: CanvasProject | null, local: CanvasProject, remote: CanvasProject): CanvasProject {
    return {
        ...local,
        nodes: mergeListById(base?.nodes ?? null, local.nodes, remote.nodes),
        connections: mergeListById(base?.connections ?? null, local.connections, remote.connections),
        chatSessions: mergeListById(base?.chatSessions ?? null, local.chatSessions, remote.chatSessions),
        updatedAt: (Date.parse(remote.updatedAt) || 0) > (Date.parse(local.updatedAt) || 0) ? remote.updatedAt : local.updatedAt,
    };
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

const MAX_CONFLICT_RETRIES = 2;

async function saveCanvasDomain(token: string, attempt = 0) {
    const { ids, changed, skip } = planCanvasSave(useCanvasStore.getState().projects, savedProjects, savedProjectIds);
    // ponytail: deletedProjects 从不清理，每次保存都把历史删除 id 全带上。几 KB 而已，
    // 相对画布本身的体积可以忽略；真变多了再加一个"已确认删除"的清理动作。
    const deleteIds = useCanvasStore.getState().deletedProjects.map((item) => item.id);
    if (skip && !deleteIds.length) return;
    const baseRevisions: Record<string, number> = {};
    for (const entry of changed) baseRevisions[entry.project.id] = savedRevisions.get(entry.project.id) ?? 0;
    try {
        const result = await saveCanvasProjects(
            changed.map((entry) => entry.project),
            ids,
            deleteIds,
            baseRevisions,
            token,
        );
        commitCanvasSave(changed, ids, result?.revisions);
    } catch (error) {
        const result = conflictResult(error);
        if (!result?.conflicts?.length || attempt >= MAX_CONFLICT_RETRIES) throw error;
        // 没冲突的画布服务端已经写进去了，先把它们的新版本记下再合并剩下的
        const written = result.revisions || {};
        commitCanvasSave(
            changed.filter((entry) => written[entry.project.id] !== undefined),
            ids,
            written,
        );
        applyCanvasConflicts(result.conflicts);
        await saveCanvasDomain(token, attempt + 1);
    }
}

function commitCanvasSave(changed: Array<{ project: CanvasProject; json: string }>, ids: string[], revisions?: Record<string, number>) {
    for (const entry of changed) savedProjects.set(entry.project.id, entry.json);
    for (const [id, revision] of Object.entries(revisions || {})) savedRevisions.set(id, revision);
    const kept = new Set(ids);
    for (const id of [...savedProjects.keys()]) if (!kept.has(id)) savedProjects.delete(id);
    for (const id of [...savedRevisions.keys()]) if (!kept.has(id)) savedRevisions.delete(id);
    savedProjectIds = ids;
}

function conflictResult(error: unknown) {
    const status = (error as { status?: number })?.status;
    if (status !== 409) return null;
    return (error as { data?: CanvasSaveResult })?.data || null;
}

/** 把服务端那份合并进本地，而不是二选一，然后带着新版本号重试。 */
function applyCanvasConflicts(conflicts: NonNullable<CanvasSaveResult["conflicts"]>) {
    const state = useCanvasStore.getState();
    const byId = new Map(conflicts.map((item) => [item.id, item]));
    const merged = state.projects.map((project) => {
        const conflict = byId.get(project.id);
        if (!conflict) return project;
        const base = savedProjects.get(project.id);
        return mergeCanvasProject(base ? (JSON.parse(base) as CanvasProject) : null, project, conflict.data as CanvasProject);
    });
    state.replaceProjects(merged, state.deletedProjects);
    for (const conflict of conflicts) {
        savedRevisions.set(conflict.id, conflict.revision);
        // base 换成服务端那份：下一轮合并要拿它当共同祖先
        savedProjects.set(conflict.id, JSON.stringify(conflict.data));
    }
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
