import { describe, expect, it } from "bun:test";

import { isChatCompletionImageModel, parseChatImagePayload } from "./image";

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
});
