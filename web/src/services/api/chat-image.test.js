import { describe, expect, it } from "bun:test";

import { isChatCompletionImageModel, isRemoteImageUrl, parseChatImagePayload } from "./image";

const PNG = "data:image/png;base64,iVBORw0KGgo=";

describe("chat-completions image models", () => {
    it("routes gemini image models away from the images endpoint", () => {
        expect(isChatCompletionImageModel("gemini-3-pro-image-preview")).toBe(true);
        expect(isChatCompletionImageModel("gemini-2.5-flash-image")).toBe(true);
        expect(isChatCompletionImageModel("nano-banana-pro")).toBe(true);
    });

    it("leaves images-endpoint models alone", () => {
        expect(isChatCompletionImageModel("imagen-3.0-generate-002")).toBe(false);
        expect(isChatCompletionImageModel("gemini-3-pro")).toBe(false);
        expect(isChatCompletionImageModel("gpt-image-2")).toBe(false);
    });
});

describe("chat image payload", () => {
    it("reads images from the markdown body", () => {
        expect(parseChatImagePayload({ choices: [{ message: { content: `好的\n\n![image](${PNG})` } }] })).toEqual([PNG]);
    });

    it("reads images from the images field", () => {
        expect(parseChatImagePayload({ choices: [{ message: { images: [{ image_url: { url: "https://cdn.test/a.png" } }] } }] })).toEqual(["https://cdn.test/a.png"]);
    });

    it("does not duplicate an image carried in both places", () => {
        expect(parseChatImagePayload({ choices: [{ message: { content: `![image](${PNG})`, images: [{ image_url: { url: PNG } }] } }] })).toEqual([PNG]);
    });

    it("surfaces the model's own refusal text when no image came back", () => {
        expect(() => parseChatImagePayload({ choices: [{ message: { content: "抱歉，我无法生成该图片" } }] })).toThrow("抱歉，我无法生成该图片");
    });

    it("surfaces gateway errors", () => {
        expect(() => parseChatImagePayload({ code: 1, msg: "无效的令牌" })).toThrow("无效的令牌");
        expect(() => parseChatImagePayload({ error: { message: "model not found" } })).toThrow("model not found");
    });

    // 4K生图渠道的真实响应：markdown 链接文字带说明、URL 是含查询参数的预签名地址。
    it("extracts the full presigned url from a real seller response", () => {
        const url =
            "https://46a49f5f12ab7c128be855497554b6ec.r2.cloudflarestorage.com/img1/photos/20260822/20260822_005840_9b0671b393_1.jpg?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=1825bf44ea2d7fa30088e29673cbdc46%2F20260821%2Fauto%2Fs3%2Faws4_request&X-Amz-Date=20260821T165929Z&X-Amz-Expires=18000&X-Amz-SignedHeaders=host&X-Amz-Signature=b0505826bedc3bca099a3ee9fb167e4418cb844b0490dbd8a969f03b441cd248";
        const payload = {
            id: "20260822_005841_0974b4c4ce",
            object: "chat.completion",
            model: "gemini-3-pro-image",
            choices: [{ index: 0, message: { role: "assistant", content: `![原图链接5小时有效](${url})`, refusal: null }, finish_reason: "stop" }],
        };
        expect(parseChatImagePayload(payload)).toEqual([url]);
        expect(isRemoteImageUrl(url)).toBe(true);
    });

    // 卖家返回的 http(s) 外链要走后端代理下载，data: 直接可用。
    it("tells seller-hosted links apart from inline data urls", () => {
        expect(isRemoteImageUrl("https://cdn.test/a.jpg?X-Amz-Signature=abc")).toBe(true);
        expect(isRemoteImageUrl("http://cdn.test/a.jpg")).toBe(true);
        expect(isRemoteImageUrl(PNG)).toBe(false);
    });
});
