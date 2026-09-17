import { describe, expect, it } from "bun:test";

import { hasResumableImageTask, resetInterruptedGeneration, resumableImageSlots } from "./canvas-generation-helpers";

const imageNode = (images, metadata = {}) => ({
    id: "n1",
    type: "image",
    title: "n1",
    position: { x: 0, y: 0 },
    width: 10,
    height: 10,
    metadata: { status: "loading", images, ...metadata },
});

describe("resumableImageSlots", () => {
    it("只认还在生成、带任务号且还没有图的槽", () => {
        const node = imageNode([
            { id: "a", status: "loading", content: "", taskId: "t-a" },
            { id: "b", status: "loading", content: "" },
            { id: "c", status: "loading", content: "https://x/y.png", taskId: "t-c" },
            { id: "d", status: "success", content: "https://x/z.png", taskId: "t-d" },
        ]);
        expect(resumableImageSlots(node).map((slot) => slot.id)).toEqual(["a"]);
    });

    it("没有 images 的节点不报错", () => {
        expect(resumableImageSlots(imageNode(undefined))).toEqual([]);
    });
});

describe("hasResumableImageTask", () => {
    it("节点级任务号也算可恢复", () => {
        expect(hasResumableImageTask(imageNode(undefined, { imageTaskId: "t-1" }))).toBe(true);
    });

    it("已经拿到图就不再恢复", () => {
        expect(hasResumableImageTask(imageNode(undefined, { imageTaskId: "t-1", content: "https://x/y.png" }))).toBe(false);
    });

    it("有可恢复的槽也算", () => {
        expect(hasResumableImageTask(imageNode([{ id: "a", status: "loading", content: "", taskId: "t-a" }]))).toBe(true);
    });

    it("已经失败的节点不算，否则会去续一个早就结束的任务", () => {
        expect(hasResumableImageTask(imageNode([{ id: "a", status: "loading", content: "", taskId: "t-a" }], { status: "error" }))).toBe(false);
    });

    it("没有任务号就不算", () => {
        expect(hasResumableImageTask(imageNode([{ id: "a", status: "loading", content: "" }]))).toBe(false);
    });
});

describe("resetInterruptedGeneration", () => {
    it("带任务号的图片节点保持等待，刷新后还能接着轮询", () => {
        const node = imageNode([{ id: "a", status: "loading", content: "", taskId: "t-a" }]);
        const [result] = resetInterruptedGeneration([node]);
        expect(result.metadata.status).toBe("loading");
        expect(result.metadata.images[0].status).toBe("loading");
    });

    it("没有任务号的照旧标记为已中断", () => {
        const node = imageNode([{ id: "a", status: "loading", content: "" }]);
        const [result] = resetInterruptedGeneration([node]);
        expect(result.metadata.status).toBe("error");
        expect(result.metadata.images[0].status).toBe("error");
    });

    it("同一节点里只有没任务号的那个槽被判中断", () => {
        const node = imageNode([
            { id: "a", status: "loading", content: "", taskId: "t-a" },
            { id: "b", status: "loading", content: "" },
        ]);
        const [result] = resetInterruptedGeneration([node]);
        expect(result.metadata.status).toBe("loading");
        expect(result.metadata.images.find((slot) => slot.id === "a").status).toBe("loading");
        expect(result.metadata.images.find((slot) => slot.id === "b").status).toBe("error");
    });
});
