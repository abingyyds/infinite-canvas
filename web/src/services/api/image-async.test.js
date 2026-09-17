import { describe, expect, it } from "bun:test";

import { asyncImageTaskFailure, imagePollTimedOut, imagesFromTask, isAsyncFacadeUnsupported, resolveTaskMediaUrl } from "./image-async";

describe("resolveTaskMediaUrl", () => {
    it("网关给的绝对地址原样用", () => {
        expect(resolveTaskMediaUrl("https://gw.example.com/v1/assets/abc?token=t", "https://api.example.com/v1")).toBe("https://gw.example.com/v1/assets/abc?token=t");
    });

    it("网关没配 ServerAddress 时给的是相对路径，用 baseUrl 的 origin 补全", () => {
        expect(resolveTaskMediaUrl("/v1/assets/abc?token=t", "https://api.example.com/v1")).toBe("https://api.example.com/v1/assets/abc?token=t");
    });

    it("空值返回空串", () => {
        expect(resolveTaskMediaUrl("", "https://api.example.com/v1")).toBe("");
    });
});

describe("imagesFromTask", () => {
    const base = "https://api.example.com/v1";

    it("优先用归档后的链接，画布因此存的是 URL 而不是几 MB 的 base64", () => {
        const images = imagesFromTask({ status: "completed", result: { images: [{ index: 0, url: "/v1/assets/a?token=t" }] } }, base);
        expect(images).toHaveLength(1);
        expect(images[0].dataUrl).toBe("https://api.example.com/v1/assets/a?token=t");
    });

    it("上游没归档时退回内联 base64", () => {
        const images = imagesFromTask({ status: "completed", result: { images: [{ index: 0, b64_json: "QUJD" }] } }, base);
        expect(images[0].dataUrl).toBe("data:image/png;base64,QUJD");
    });

    it("多张图按顺序返回且 id 各不相同", () => {
        const images = imagesFromTask(
            {
                status: "completed",
                result: {
                    images: [
                        { index: 0, url: "/v1/assets/a" },
                        { index: 1, url: "/v1/assets/b" },
                    ],
                },
            },
            base,
        );
        expect(images.map((item) => item.dataUrl)).toEqual(["https://api.example.com/v1/assets/a", "https://api.example.com/v1/assets/b"]);
        expect(images[0].id).not.toBe(images[1].id);
    });

    it("成功了却没有图要报错，不能静默返回空数组", () => {
        expect(() => imagesFromTask({ status: "completed", result: { images: [] } }, base)).toThrow();
    });
});

describe("asyncImageTaskFailure", () => {
    it("失败时给出服务端的原因", () => {
        expect(asyncImageTaskFailure({ status: "failed", error: { message: "No available compatible accounts" } })).toBe("No available compatible accounts");
    });

    it("没带原因时也要有一句话", () => {
        expect(asyncImageTaskFailure({ status: "failed" })).toBeTruthy();
    });

    it("未结束的任务不算失败", () => {
        expect(asyncImageTaskFailure({ status: "processing" })).toBe("");
        expect(asyncImageTaskFailure({ status: "pending" })).toBe("");
    });
});

describe("isAsyncFacadeUnsupported", () => {
    it("404 说明这个网关没有异步门面", () => {
        expect(isAsyncFacadeUnsupported({ response: { status: 404 } })).toBe(true);
    });

    it("渠道类型不支持时网关回 400 并说明原因", () => {
        expect(isAsyncFacadeUnsupported({ response: { status: 400, data: { error: { message: "async facade does not support this channel api type: 2" } } } })).toBe(true);
    });

    it("异步门面的 multipart 编辑要求网关有对象存储，没有时也要退回同步", () => {
        expect(isAsyncFacadeUnsupported({ response: { status: 400, data: { error: { message: "multipart image edits on the async facade require media storage; send image_urls in a JSON body instead" } } } })).toBe(true);
    });

    it("上游真报错不能被当成不支持，否则会退回同步重跑一次白花钱", () => {
        expect(isAsyncFacadeUnsupported({ response: { status: 400, data: { error: { message: "invalid size" } } } })).toBe(false);
        expect(isAsyncFacadeUnsupported({ response: { status: 503 } })).toBe(false);
    });
});

describe("imagePollTimedOut", () => {
    it("按耗时计预算", () => {
        expect(imagePollTimedOut(0, 60_000)).toBe(false);
        expect(imagePollTimedOut(0, 16 * 60_000)).toBe(true);
    });
});
