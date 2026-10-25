import { afterEach, describe, expect, it } from "bun:test";
import axios, { AxiosError } from "axios";

import i18n from "@/i18n";
import { defaultConfig } from "@/stores/use-config-store";
import { useUserStore } from "@/stores/use-user-store";
import { pendingImageDownload, rememberImageDownload } from "@/lib/canvas/canvas-image-download";
import { requestGeneration } from "./image";
import { downloadGeneratedImage } from "./image-download";

const originalAdapter = axios.defaults.adapter;
const originalFileReader = globalThis.FileReader;
const originalToken = useUserStore.getState().token;
const sourceUrl = "https://images.example.test/generated.png?signature=saved";

afterEach(() => {
    axios.defaults.adapter = originalAdapter;
    globalThis.FileReader = originalFileReader;
    useUserStore.setState({ token: originalToken });
});

describe("generated image download recovery", () => {
    it("先保存已付费图片链接，401 后重新登录重试只下载，不再调用模型", async () => {
        const requests = [];
        let signedIn = false;
        let node = { id: "node", metadata: { status: "error", images: [{ id: "slot", status: "error", content: "" }] } };
        axios.defaults.adapter = async (config) => {
            requests.push({ url: config.url, data: JSON.parse(config.data), token: config.headers.get("Authorization") });
            if (config.url.endsWith("/chat/completions")) {
                return { status: 200, data: { choices: [{ message: { content: `![image](${sourceUrl})` } }] }, headers: {}, config };
            }
            expect(config.url).toBe("/api/media-content");
            // 下载开始前，源地址必须已经进入节点数据。
            expect(pendingImageDownload(node, "slot")).toEqual({ url: sourceUrl, viaProxy: true });
            if (!signedIn) throw new AxiosError("Unauthorized", "ERR_BAD_REQUEST", config, null, { status: 401, data: { code: 1, msg: "未登录" } });
            return { status: 200, data: new Blob(["png"], { type: "image/png" }), headers: {}, config };
        };
        globalThis.FileReader = class {
            readAsDataURL() {
                this.result = "data:image/png;base64,cG5n";
                this.onload();
            }
        };
        const config = { ...defaultConfig, model: "default::gemini-3-pro-image-preview", count: "1" };
        await expect(requestGeneration(config, "a landscape", {
            onImageGenerated: (source) => { node = rememberImageDownload(node, source, "slot"); },
        })).rejects.toThrow(i18n.t("imageDownload.loginRequired"));

        // 模拟页面刷新：重试依据持久化后的节点，而不是内存里的异常对象。
        node = JSON.parse(JSON.stringify(node));
        signedIn = true;
        useUserStore.setState({ token: "new-site-session" });
        expect(await downloadGeneratedImage(pendingImageDownload(node, "slot"))).toBe("data:image/png;base64,cG5n");
        expect(requests.filter((request) => request.url.endsWith("/chat/completions"))).toHaveLength(1);
        expect(requests.filter((request) => request.url === "/api/media-content")).toHaveLength(2);
        expect(requests.at(-1).data).toEqual({ url: sourceUrl });
        expect(requests.at(-1).token).toBe("Bearer new-site-session");
    });

    it("原图链接失效时保留下载语义，不自动生成替代图片", async () => {
        let calls = 0;
        axios.defaults.adapter = async (config) => {
            calls++;
            expect(config.url).toBe("/api/media-content");
            throw new AxiosError("Gone", "ERR_BAD_REQUEST", config, null, { status: 410 });
        };
        const source = { url: sourceUrl, viaProxy: true };
        await expect(downloadGeneratedImage(source)).rejects.toThrow(i18n.t("imageDownload.linkUnavailable"));
        expect(source.url).toBe(sourceUrl);
        expect(calls).toBe(1);
    });

    it("异步任务返回图片后也会在保存图片之前记录原始链接", async () => {
        const requests = [];
        let source;
        axios.defaults.adapter = async (config) => {
            requests.push(config.url);
            const data = config.url.endsWith("/images/generations/async")
                ? { id: "already-paid-task" }
                : { status: "completed", result: { images: [{ url: sourceUrl }] } };
            return { status: 200, data, headers: {}, config };
        };
        const images = await requestGeneration(defaultConfig, "a landscape", { onImageGenerated: (value) => { source = value; } });
        expect(images[0].dataUrl).toBe(sourceUrl);
        expect(source).toEqual({ url: sourceUrl, viaProxy: false });
        const count = requests.length;
        expect(await downloadGeneratedImage(source)).toBe(sourceUrl);
        expect(requests).toHaveLength(count);
    });

    it("用户取消下载会继续传播取消，不误报登录失效", async () => {
        const controller = new AbortController();
        controller.abort();
        await expect(downloadGeneratedImage({ url: sourceUrl, viaProxy: true }, { signal: controller.signal })).rejects.toBeInstanceOf(axios.CanceledError);
    });
});
