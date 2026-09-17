import { describe, expect, it } from "bun:test";

import { chainByKey, mergeCanvasProject, planCanvasSave } from "./user-data-sync";

const project = (id, title) => ({ id, title, nodes: [] });

const savedFrom = (...projects) => new Map(projects.map((item) => [item.id, JSON.stringify(item)]));

describe("planCanvasSave", () => {
    it("只上传内容变化的那一个画布", () => {
        const a = project("a", "A");
        const b = project("b", "B");
        const saved = savedFrom(a, b);
        const plan = planCanvasSave([project("a", "A changed"), b], saved, ["a", "b"]);
        expect(plan.skip).toBe(false);
        expect(plan.changed.map((entry) => entry.project.id)).toEqual(["a"]);
        expect(plan.ids).toEqual(["a", "b"]);
    });

    it("完全没变时跳过请求", () => {
        const a = project("a", "A");
        const plan = planCanvasSave([a], savedFrom(a), ["a"]);
        expect(plan.skip).toBe(true);
        expect(plan.changed).toHaveLength(0);
    });

    it("画布被删除时不跳过，并把剩余 id 全量报给服务端", () => {
        const a = project("a", "A");
        const b = project("b", "B");
        const plan = planCanvasSave([a], savedFrom(a, b), ["a", "b"]);
        expect(plan.skip).toBe(false);
        expect(plan.changed).toHaveLength(0);
        expect(plan.ids).toEqual(["a"]);
    });

    it("只是顺序变了也要上报，否则服务端排序会停留在旧顺序", () => {
        const a = project("a", "A");
        const b = project("b", "B");
        const plan = planCanvasSave([b, a], savedFrom(a, b), ["a", "b"]);
        expect(plan.skip).toBe(false);
        expect(plan.ids).toEqual(["b", "a"]);
    });

    it("新建的画布算作变更", () => {
        const a = project("a", "A");
        const plan = planCanvasSave([a, project("new", "N")], savedFrom(a), ["a"]);
        expect(plan.changed.map((entry) => entry.project.id)).toEqual(["new"]);
        expect(plan.ids).toEqual(["a", "new"]);
    });
});

describe("chainByKey", () => {
    it("同一 key 的任务串行执行，后发的不会先于前一个完成", async () => {
        const chains = new Map();
        const order = [];
        let releaseFirst;
        const gate = new Promise((resolve) => (releaseFirst = resolve));
        const first = chainByKey(chains, "canvas", async () => {
            await gate;
            order.push("first");
        });
        const second = chainByKey(chains, "canvas", async () => {
            order.push("second");
        });
        releaseFirst();
        await Promise.all([first, second]);
        expect(order).toEqual(["first", "second"]);
    });

    it("前一个任务失败不会掐断后面的队列", async () => {
        const chains = new Map();
        const order = [];
        const failing = chainByKey(chains, "canvas", async () => {
            order.push("failing");
            throw new Error("boom");
        });
        await failing.catch(() => undefined);
        await chainByKey(chains, "canvas", async () => {
            order.push("after");
        });
        expect(order).toEqual(["failing", "after"]);
    });

    it("不同 key 互不阻塞", async () => {
        const chains = new Map();
        const order = [];
        let releaseCanvas;
        const gate = new Promise((resolve) => (releaseCanvas = resolve));
        const canvas = chainByKey(chains, "canvas", async () => {
            await gate;
            order.push("canvas");
        });
        await chainByKey(chains, "assets", async () => {
            order.push("assets");
        });
        releaseCanvas();
        await canvas;
        expect(order).toEqual(["assets", "canvas"]);
    });
});

const node = (id, title = id) => ({ id, type: "image", title, position: { x: 0, y: 0 }, width: 1, height: 1 });
const canvas = (nodes, extra = {}) => ({ id: "p", title: "P", nodes, connections: [], chatSessions: [], updatedAt: "2026-01-01T00:00:00Z", ...extra });
const nodeIds = (project) => project.nodes.map((item) => item.id);

describe("mergeCanvasProject", () => {
    it("本地新增的节点不会被服务端的旧版本冲掉", () => {
        const base = canvas([node("a")]);
        const local = canvas([node("a"), node("b"), node("c")]);
        const remote = canvas([node("a")]);
        expect(nodeIds(mergeCanvasProject(base, local, remote))).toEqual(["a", "b", "c"]);
    });

    it("服务端新增的节点会被带回来", () => {
        const base = canvas([node("a")]);
        const local = canvas([node("a")]);
        const remote = canvas([node("a"), node("x")]);
        expect(nodeIds(mergeCanvasProject(base, local, remote))).toEqual(["a", "x"]);
    });

    it("本地删掉的节点不会被服务端复活", () => {
        const base = canvas([node("a"), node("b")]);
        const local = canvas([node("a")]);
        const remote = canvas([node("a"), node("b")]);
        expect(nodeIds(mergeCanvasProject(base, local, remote))).toEqual(["a"]);
    });

    it("服务端删掉的节点在本地也消失", () => {
        const base = canvas([node("a"), node("b")]);
        const local = canvas([node("a"), node("b")]);
        const remote = canvas([node("a")]);
        expect(nodeIds(mergeCanvasProject(base, local, remote))).toEqual(["a"]);
    });

    it("同一个节点两边都改过时保留本地的改动", () => {
        const base = canvas([node("a", "原始")]);
        const local = canvas([node("a", "本地")]);
        const remote = canvas([node("a", "远端")]);
        expect(mergeCanvasProject(base, local, remote).nodes[0].title).toBe("本地");
    });

    it("没有 base 时两边都保留，宁可多留也不丢", () => {
        const local = canvas([node("a"), node("b")]);
        const remote = canvas([node("a"), node("x")]);
        expect(nodeIds(mergeCanvasProject(null, local, remote))).toEqual(["a", "b", "x"]);
    });

    it("updatedAt 取两边较晚的那个", () => {
        const base = canvas([node("a")]);
        const local = canvas([node("a")], { updatedAt: "2026-01-01T00:00:00Z" });
        const remote = canvas([node("a")], { updatedAt: "2026-02-01T00:00:00Z" });
        expect(mergeCanvasProject(base, local, remote).updatedAt).toBe("2026-02-01T00:00:00Z");
    });

    it("连线和会话同样按 id 三方合并", () => {
        const base = canvas([], { connections: [{ id: "c1" }], chatSessions: [{ id: "s1" }] });
        const local = canvas([], { connections: [{ id: "c1" }, { id: "c2" }], chatSessions: [{ id: "s1" }] });
        const remote = canvas([], { connections: [{ id: "c1" }], chatSessions: [{ id: "s1" }, { id: "s2" }] });
        const merged = mergeCanvasProject(base, local, remote);
        expect(merged.connections.map((item) => item.id)).toEqual(["c1", "c2"]);
        expect(merged.chatSessions.map((item) => item.id)).toEqual(["s1", "s2"]);
    });
});
