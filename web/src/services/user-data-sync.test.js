import { describe, expect, it } from "bun:test";

import { planCanvasSave } from "./user-data-sync";

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
