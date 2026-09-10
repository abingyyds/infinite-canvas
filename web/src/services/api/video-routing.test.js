import { describe, expect, it } from "bun:test";

import { isUnifiedJsonVideoModel, unifiedVideoDuration, unwrapEnvelope, videoCreatePath, videoPollTimedOut } from "./video";
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

describe("unified JSON video duration", () => {
    it("sends the chosen seconds untouched; each gateway model has its own range", () => {
        expect(unifiedVideoDuration("30")).toBe(30);
        expect(unifiedVideoDuration("4")).toBe(4);
        expect(unifiedVideoDuration("12.7")).toBe(12);
    });

    it("falls back to 6 seconds for adaptive or empty input", () => {
        expect(unifiedVideoDuration("-1")).toBe(6);
        expect(unifiedVideoDuration("")).toBe(6);
    });
});

describe("task response envelope", () => {
    it("unwraps a real envelope", () => {
        expect(unwrapEnvelope({ code: 0, data: { id: "task_1" } }, "empty")).toEqual({ id: "task_1" });
    });

    it("keeps a flat task body that merely carries code: 0", () => {
        // 商家的轮询响应把任务字段平铺在 code 同级，没有 data 信封
        const flat = { code: 0, success: true, task_id: "vid_1", status: "processing", video_url: null };
        expect(unwrapEnvelope(flat, "empty")).toEqual(flat);
    });

    it("still rejects an envelope with no payload", () => {
        expect(() => unwrapEnvelope({ code: 0, data: null }, "empty")).toThrow("empty");
        expect(() => unwrapEnvelope({ code: 0 }, "empty")).toThrow("empty");
    });

    it("still surfaces a non-zero code as an error", () => {
        expect(() => unwrapEnvelope({ code: 400, message: "bad params" }, "empty")).toThrow("bad params");
    });
});

describe("video poll budget", () => {
    it("keeps polling well past the old ~5 minute ceiling", () => {
        const startedAt = 1_000_000;
        // 商家标称 15 分钟出片，7 分钟就放弃会让用户白付一次任务的钱
        expect(videoPollTimedOut(startedAt, startedAt + 7 * 60_000)).toBe(false);
        expect(videoPollTimedOut(startedAt, startedAt + 15 * 60_000)).toBe(false);
    });

    it("does give up eventually", () => {
        const startedAt = 1_000_000;
        expect(videoPollTimedOut(startedAt, startedAt + 25 * 60_000)).toBe(true);
    });
});
