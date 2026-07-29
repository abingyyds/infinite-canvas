import { describe, expect, it } from "bun:test";

import { shouldRetryImageEditAsJson, supportsImageFormatParams } from "./image";

describe("image edit JSON fallback trigger", () => {
    it("retries on gateway upload rejections", () => {
        expect(shouldRetryImageEditAsJson("Image upload failed, please retry")).toBe(true);
        expect(shouldRetryImageEditAsJson("please check the image and try again")).toBe(true);
        expect(shouldRetryImageEditAsJson("invalid image data")).toBe(true);
        expect(shouldRetryImageEditAsJson("unsupported image format")).toBe(true);
        expect(shouldRetryImageEditAsJson("图片上传失败，请稍后重试")).toBe(true);
        expect(shouldRetryImageEditAsJson("图片格式不受支持")).toBe(true);
    });

    it("does not retry on unrelated failures", () => {
        // 重试要花第二次调用的钱，鉴权/额度/限流这类错误重试没有意义
        expect(shouldRetryImageEditAsJson("鉴权失败，请检查 API Key")).toBe(false);
        expect(shouldRetryImageEditAsJson("请求被限流或额度不足，请稍后重试")).toBe(false);
        expect(shouldRetryImageEditAsJson("model gpt-image-2 not found")).toBe(false);
        expect(shouldRetryImageEditAsJson("")).toBe(false);
    });
});

describe("image format params", () => {
    it("omits them for gpt-image models", () => {
        expect(supportsImageFormatParams("gpt-image-2-4K")).toBe(false);
        expect(supportsImageFormatParams("gpt-image-2")).toBe(false);
        expect(supportsImageFormatParams("gpt-image-1")).toBe(false);
    });

    it("keeps them for models that do accept them", () => {
        expect(supportsImageFormatParams("dall-e-3")).toBe(true);
        expect(supportsImageFormatParams("seedream-4-0")).toBe(true);
    });
});
