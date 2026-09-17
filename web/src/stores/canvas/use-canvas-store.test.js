import { describe, expect, it } from "bun:test";

import { useCanvasStore } from "./use-canvas-store";

const read = (id) => useCanvasStore.getState().projects.find((project) => project.id === id);

describe("updateProjectViewport", () => {
    it("平移缩放不刷新 updatedAt，否则旧内容会带着新时间戳赢过同步合并", () => {
        const id = useCanvasStore.getState().createProject("viewport-only");
        const before = read(id).updatedAt;
        useCanvasStore.getState().updateProjectViewport(id, { x: 10, y: 20, k: 2 });
        expect(read(id).viewport).toEqual({ x: 10, y: 20, k: 2 });
        expect(read(id).updatedAt).toBe(before);
    });

    it("内容变更仍然刷新 updatedAt", async () => {
        const id = useCanvasStore.getState().createProject("content-change");
        const before = read(id).updatedAt;
        await new Promise((resolve) => setTimeout(resolve, 2));
        useCanvasStore.getState().updateProject(id, { nodes: [{ id: "n1" }] });
        expect(read(id).nodes).toHaveLength(1);
        expect(read(id).updatedAt).not.toBe(before);
    });
});
