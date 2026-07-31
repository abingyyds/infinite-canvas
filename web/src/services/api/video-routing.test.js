import { describe, expect, it } from "bun:test";

import { isUnifiedJsonVideoModel, videoCreatePath } from "./video";
import { isSeedanceVideoModel } from "@/lib/seedance-video";

const xai = { baseUrl: "https://api.x.ai" };
const subrouter = { baseUrl: "https://subrouter.example.com/v1" };
const gateway = { baseUrl: "/api" };

describe("grok video create path", () => {
    it("uses the first-frame JSON endpoint on the built-in gateway", () => {
        expect(videoCreatePath(gateway, "grok-video-1.5", true)).toBe("/videos/generations");
        expect(videoCreatePath(gateway, "grok-imagine-video", true)).toBe("/videos/generations");
    });

    it("uses the first-frame JSON endpoint when talking to xAI directly", () => {
        expect(videoCreatePath(xai, "grok-imagine-video-1.5", false)).toBe("/videos/generations");
    });

    it("falls back to the legacy /videos endpoint on other compatible gateways", () => {
        expect(videoCreatePath(subrouter, "grok-imagine-video", false)).toBe("/videos");
    });

    it("leaves non-grok models on /videos", () => {
        expect(videoCreatePath(xai, "sora-2", true)).toBe("/videos");
    });
});

describe("unified JSON video routing", () => {
    it("treats SubRouter seedance as an OpenAI-style unified video model", () => {
        expect(isUnifiedJsonVideoModel("seedance-1.0-pro")).toBe(true);
        expect(isUnifiedJsonVideoModel("seedance-2.0-480p")).toBe(true);
        // 不能被当成火山方舟原生任务，否则会打到 /contents/generations/tasks
        expect(isSeedanceVideoModel("seedance-1.0-pro")).toBe(false);
    });

    it("covers veo and omni, which reject the multipart body", () => {
        expect(isUnifiedJsonVideoModel("veo-3-1-fast")).toBe(true);
        expect(isUnifiedJsonVideoModel("veo-3-1-ref")).toBe(true);
        expect(isUnifiedJsonVideoModel("omni-fast")).toBe(true);
        expect(isUnifiedJsonVideoModel("omni-v2v-no-water")).toBe(true);
    });

    it("keeps ark-native doubao-seedance on the ark task API", () => {
        expect(isUnifiedJsonVideoModel("doubao-seedance-1-0-pro-250528")).toBe(false);
        expect(isSeedanceVideoModel("doubao-seedance-1-0-pro-250528")).toBe(true);
    });

    it("leaves unrelated video models off the unified branch", () => {
        expect(isUnifiedJsonVideoModel("sora-2")).toBe(false);
        expect(isUnifiedJsonVideoModel("grok-imagine-video")).toBe(false);
    });
});
