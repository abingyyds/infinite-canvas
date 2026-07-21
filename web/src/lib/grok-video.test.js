import { describe, expect, it } from "bun:test";

import { isGrokImagineVideoModel, isGrokPreviewVideoModel } from "./grok-video";

describe("grok video model detection", () => {
    it("matches official grok imagine names", () => {
        expect(isGrokImagineVideoModel("grok-imagine-video")).toBe(true);
        expect(isGrokPreviewVideoModel("grok-imagine-video-1.5")).toBe(true);
        expect(isGrokPreviewVideoModel("grok-imagine-video-1.5-preview")).toBe(true);
    });

    it("matches the SubRouter grok-video-1.5 name", () => {
        expect(isGrokPreviewVideoModel("grok-video-1.5")).toBe(true);
        expect(isGrokImagineVideoModel("grok-video-1.5")).toBe(true);
    });

    it("does not match other video models", () => {
        expect(isGrokPreviewVideoModel("grok-video")).toBe(false);
        expect(isGrokImagineVideoModel("grok-video")).toBe(false);
        expect(isGrokPreviewVideoModel("sora-2")).toBe(false);
        expect(isGrokImagineVideoModel("seedance-2.0")).toBe(false);
    });
});
