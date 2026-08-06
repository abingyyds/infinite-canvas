import { describe, expect, it } from "bun:test";

import { normalizeResolution, resolveRatioSize } from "./image";

describe("resolution tiers", () => {
    it("only accepts the three tiers", () => {
        expect(normalizeResolution("4k")).toBe("4k");
        expect(normalizeResolution("2K")).toBe("2k");
        expect(normalizeResolution("auto")).toBeUndefined();
        expect(normalizeResolution("")).toBeUndefined();
        expect(normalizeResolution(undefined)).toBeUndefined();
    });
});

describe("ratio + resolution to pixels", () => {
    it("reproduces the fixed sizes the split-out buttons used to send", () => {
        expect(resolveRatioSize("4k", "16:9")).toBe("3840x2160");
        expect(resolveRatioSize("4k", "9:16")).toBe("2160x3840");
        expect(resolveRatioSize("2k", "16:9")).toBe("2048x1152");
        expect(resolveRatioSize("2k", "9:16")).toBe("1152x2048");
        expect(resolveRatioSize("2k", "1:1")).toBe("2048x2048");
    });

    it("keeps auto on the 1024 short side", () => {
        expect(resolveRatioSize("auto", "16:9")).toBe("1824x1024");
        expect(resolveRatioSize(undefined, "1:1")).toBe("1024x1024");
        expect(resolveRatioSize("auto", "9:16")).toBe("1024x1824");
    });

    it("clamps 4k on square-ish ratios to stay under the pixel ceiling", () => {
        // 3840x3840 会到 1474 万像素，超过 8294400 的上限，长边必须被压回 2880
        expect(resolveRatioSize("4k", "1:1")).toBe("2880x2880");
        const [width, height] = resolveRatioSize("4k", "4:3").split("x").map(Number);
        expect(width * height).toBeLessThanOrEqual(8294400);
        expect(width).toBeGreaterThan(2880);
    });

    it("lifts 1k on wide ratios to stay above the pixel floor", () => {
        // 1024x576 只有 58 万像素，低于 655360 的下限
        const [width, height] = resolveRatioSize("1k", "16:9").split("x").map(Number);
        expect(width * height).toBeGreaterThanOrEqual(655360);
        expect(width % 16).toBe(0);
        expect(height % 16).toBe(0);
    });
});
