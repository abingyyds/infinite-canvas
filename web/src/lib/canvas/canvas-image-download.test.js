import { describe, expect, it } from "bun:test";

import { applyDownloadedImage, pendingImageDownload, rememberImageDownload } from "./canvas-image-download";
import { resetInterruptedGeneration } from "./canvas-generation-helpers";

const source = { url: "https://images.example.test/result.png", viaProxy: true };
const uploaded = { url: "blob:downloaded", storageKey: "image:saved", width: 1200, height: 800, bytes: 24, mimeType: "image/png" };
const node = (images, metadata = {}) => ({ id: "n", type: "image", position: { x: 5, y: 10 }, width: 300, height: 300, metadata: { status: "loading", images, ...metadata } });
const slot = (id, extra = {}) => ({ id, content: "", status: "error", ...extra });

describe("canvas image download retry", () => {
    it("单图节点记录的链接在刷新后仍可只重新下载", () => {
        const saved = JSON.parse(JSON.stringify(rememberImageDownload(node(), source)));
        const [restored] = resetInterruptedGeneration([saved]);
        expect(pendingImageDownload(restored)).toEqual(source);
        expect(restored.metadata.errorDetails).toContain("下载");
        const completed = applyDownloadedImage(restored, uploaded);
        expect(completed.metadata.content).toBe(uploaded.url);
        expect(completed.metadata.pendingImageDownload).toBeUndefined();
        expect(completed.width / completed.height).toBe(1.5);
    });

    it("全部槽位失败时不指定槽位的重试也会找到已生成的链接", () => {
        const saved = rememberImageDownload(node([slot("a"), slot("b")]), source, "b");
        expect(pendingImageDownload(saved)).toEqual(source);
        expect(pendingImageDownload(saved, "a")).toBeUndefined();
    });

    it("下载一个失败槽位不会替换已成功的主图", () => {
        const primary = slot("a", { status: "success", content: "blob:primary" });
        const saved = rememberImageDownload(node([primary, slot("b")], { content: "blob:primary", primaryImageId: "a" }), source, "b");
        const completed = applyDownloadedImage(saved, uploaded, "b");
        expect(completed.metadata.content).toBe("blob:primary");
        expect(completed.metadata.primaryImageId).toBe("a");
        expect(completed.metadata.images[0]).toEqual(primary);
        expect(completed.metadata.images[1].content).toBe(uploaded.url);
        expect(completed.metadata.images[1].pendingImageDownload).toBeUndefined();
        expect(completed.position).toEqual(saved.position);
    });

    it("首张恢复的图片成为主图，但其他还在下载的槽位保持 loading", () => {
        const saved = rememberImageDownload(node([slot("a"), slot("b", { status: "loading" })]), source, "a");
        const completed = applyDownloadedImage(saved, uploaded, "a");
        expect(completed.metadata.primaryImageId).toBe("a");
        expect(completed.metadata.content).toBe(uploaded.url);
        expect(completed.metadata.status).toBe("loading");
        expect(completed.metadata.images[1].status).toBe("loading");
    });
});
