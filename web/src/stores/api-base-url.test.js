import { describe, expect, it } from "bun:test";

import { normalizeApiBaseUrl } from "./use-config-store";

describe("api base url normalization", () => {
    it("keeps a plain host and a correct /v1 base", () => {
        expect(normalizeApiBaseUrl("https://api.example.com")).toBe("https://api.example.com");
        expect(normalizeApiBaseUrl("https://api.example.com/v1")).toBe("https://api.example.com/v1");
        expect(normalizeApiBaseUrl("https://api.example.com/v1/")).toBe("https://api.example.com/v1");
    });

    it("trims what follows the version segment", () => {
        // 用户多填一层 /v1 时，请求会打到 /v1/v1/models，上游返回 404 接口不存在
        expect(normalizeApiBaseUrl("https://api.example.com/v1/v1")).toBe("https://api.example.com/v1");
        // 直接粘贴完整端点地址也很常见
        expect(normalizeApiBaseUrl("https://api.example.com/v1/chat/completions")).toBe("https://api.example.com/v1");
    });

    it("keeps ark-style bases intact", () => {
        expect(normalizeApiBaseUrl("https://ark.cn-beijing.volces.com/api/v3")).toBe("https://ark.cn-beijing.volces.com/api/v3");
        expect(normalizeApiBaseUrl("https://ark.cn-beijing.volces.com/api/v3/chat/completions")).toBe("https://ark.cn-beijing.volces.com/api/v3");
    });

    it("leaves relative and non-url values alone", () => {
        expect(normalizeApiBaseUrl("/api")).toBe("/api");
        expect(normalizeApiBaseUrl("")).toBe("");
    });
});
